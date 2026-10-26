package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"time"

	"github.com/condatainer/condatainer/internal/config"
	"github.com/condatainer/condatainer/internal/helper"
)

// managerOffer is a helper a source offers. State is "added", "newer" (added, and a newer version is offered) or "".
type managerOffer struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	State       string `json:"state,omitempty"`
}

type managerScript struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Added       bool   `json:"added,omitempty"`
	Newer       bool   `json:"newer,omitempty"`
	HiddenBy    string `json:"hidden_by,omitempty"`
}

type managerDir struct {
	Dir     string          `json:"dir"`
	Layer   string          `json:"layer"`
	Scripts []managerScript `json:"scripts"`
}

type managerResp struct {
	Offered      []managerOffer `json:"offered"`
	OfferedError string         `json:"offered_error,omitempty"`
	Notes        string         `json:"notes,omitempty"`
	Installed    []managerDir   `json:"installed"`
	Layers       []string       `json:"layers"` // layers a helper can be added to
}

// handleHelperManager serves GET /api/helpers/manager: what the sources offer and what is installed, by directory.
//   - An index that cannot be read leaves the offered list empty with offered_error set. The installed list does not depend on it.
func (s *srv) handleHelperManager(w http.ResponseWriter, r *http.Request) {
	var notes bytes.Buffer
	offers, offerErr := helper.RefreshRemoteMetadata(r.Context(), false, &notes)
	resp := managerResp{Notes: notes.String(), Offered: []managerOffer{}, Layers: []string{}}
	if offerErr != nil {
		resp.OfferedError = offerErr.Error()
		offers = nil
	}

	// newer[dir+name] is set for an added helper the sources offer a different version of.
	newer := map[string]bool{}
	addedAnywhere, newerAnywhere := map[string]bool{}, map[string]bool{}
	if offers != nil {
		plan, err := helper.PlanUpgradeWith(offers, "", nil)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		for _, item := range plan {
			addedAnywhere[item.Name] = true
			if item.Action == helper.UpgradeReplace || item.Action == helper.UpgradeUnwritable {
				newer[item.Dir+"\x00"+item.Name] = true
				newerAnywhere[item.Name] = true
			}
		}
	}
	for name, e := range offers {
		offer := managerOffer{Name: name, Description: e.Description}
		if newerAnywhere[name] {
			offer.State = "newer"
		} else if addedAnywhere[name] {
			offer.State = "added"
		}
		resp.Offered = append(resp.Offered, offer)
	}
	sort.Slice(resp.Offered, func(i, j int) bool { return resp.Offered[i].Name < resp.Offered[j].Name })

	groups, err := helper.ListScripts()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	resp.Installed = []managerDir{}
	for _, g := range groups {
		dir := managerDir{Dir: g.Dir, Layer: string(g.Layer), Scripts: []managerScript{}}
		for _, sc := range g.Scripts {
			dir.Scripts = append(dir.Scripts, managerScript{
				Name: sc.Name, Description: sc.Description, Added: sc.Added,
				Newer: newer[g.Dir+"\x00"+sc.Name], HiddenBy: sc.HiddenBy,
			})
		}
		resp.Installed = append(resp.Installed, dir)
	}
	for _, layer := range config.WritableHelperLayers() {
		resp.Layers = append(resp.Layers, string(layer))
	}
	writeJSON(w, resp)
}

type helperChangeReq struct {
	Names  []string `json:"names"`
	Layer  string   `json:"layer"`
	Force  bool     `json:"force"`
	DryRun bool     `json:"dry_run"`
}

// decodeHelperChange reads the body of a POST that changes helpers, or writes the error and returns false.
//   - needNames is true for add and remove, which act on the names given. Upgrade with none covers every added helper.
func decodeHelperChange(w http.ResponseWriter, r *http.Request, needNames bool) (helperChangeReq, config.DataLayer, bool) {
	var req helperChangeReq
	if r.Method != http.MethodPost {
		http.Error(w, "POST required", http.StatusMethodNotAllowed)
		return req, "", false
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid body: "+err.Error(), http.StatusBadRequest)
		return req, "", false
	}
	if needNames && len(req.Names) == 0 {
		http.Error(w, "names required", http.StatusBadRequest)
		return req, "", false
	}
	var layer config.DataLayer
	if req.Layer != "" {
		var err error
		if layer, err = config.ParseDataLayer(req.Layer); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return req, "", false
		}
	}
	return req, layer, true
}

// startHelperTask runs change as a task with a progress stream, then clears the installed-helpers cache so the lists the page reloads are current.
func (s *srv) startHelperTask(w http.ResponseWriter, prefix string, change func(ctx context.Context, out io.Writer) error) {
	taskID := fmt.Sprintf("%s-%d", prefix, time.Now().UnixNano())
	broker := newSSEBroker()
	ctx, cancel := context.WithCancel(s.ctx)
	s.tasks.Store(taskID, &taskEntry{broker: broker, cancel: cancel})
	writeJSON(w, map[string]string{"id": taskID})

	go func() {
		defer cancel()
		defer s.scheduleTaskCleanup(taskID)
		err := change(ctx, &brokerWriter{broker})
		invalidateHelperInstalledCache()
		broadcastResult(broker, ctx, err)
	}()
}

// handleHelperAdd serves POST /api/helpers/add: {names, layer?, force?}.
func (s *srv) handleHelperAdd(w http.ResponseWriter, r *http.Request) {
	req, layer, ok := decodeHelperChange(w, r, true)
	if !ok {
		return
	}
	s.startHelperTask(w, "helpers-add", func(ctx context.Context, out io.Writer) error {
		return helper.Add(ctx, req.Names, layer, req.Force, out)
	})
}

// handleHelperRemove serves POST /api/helpers/remove: {names, layer?}. The page asks before it calls.
func (s *srv) handleHelperRemove(w http.ResponseWriter, r *http.Request) {
	req, layer, ok := decodeHelperChange(w, r, true)
	if !ok {
		return
	}
	s.startHelperTask(w, "helpers-remove", func(ctx context.Context, out io.Writer) error {
		items, planErr := helper.PlanRemove(req.Names, layer)
		if planErr != nil {
			fmt.Fprintf(out, "WARN: %v\n", planErr)
		}
		if err := helper.Remove(items, out); err != nil {
			return err
		}
		if planErr != nil {
			return errors.New("some helpers were not removed")
		}
		return nil
	})
}

type upgradePlanItem struct {
	Name   string `json:"name"`
	Dir    string `json:"dir"`
	Layer  string `json:"layer"`
	Action string `json:"action"`
}

// handleHelperUpgrade serves POST /api/helpers/upgrade: {names?, layer?, dry_run?}.
//   - With dry_run it returns the plan and changes nothing, for the page to show before it asks.
//   - Otherwise it replaces what the plan marks and streams progress.
func (s *srv) handleHelperUpgrade(w http.ResponseWriter, r *http.Request) {
	req, layer, ok := decodeHelperChange(w, r, false)
	if !ok {
		return
	}
	if req.DryRun {
		plan, err := helper.PlanUpgrade(r.Context(), req.Names, layer, io.Discard)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		items := make([]upgradePlanItem, 0, len(plan))
		for _, p := range plan {
			items = append(items, upgradePlanItem{Name: p.Name, Dir: p.Dir, Layer: string(p.Layer), Action: string(p.Action)})
		}
		writeJSON(w, map[string]any{"items": items})
		return
	}
	s.startHelperTask(w, "helpers-upgrade", func(ctx context.Context, out io.Writer) error {
		plan, err := helper.PlanUpgrade(ctx, req.Names, layer, out)
		if err != nil {
			return err
		}
		return helper.ApplyUpgrade(ctx, plan, out)
	})
}
