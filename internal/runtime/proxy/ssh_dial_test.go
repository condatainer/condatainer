package proxy

import (
	"errors"
	"sync"
	"testing"
	"time"
)

// fakeSSHClient answers a keepalive with reply, or never when block is set.
type fakeSSHClient struct {
	block  bool
	err    error
	closed chan struct{}
	once   sync.Once
}

func newFakeSSHClient() *fakeSSHClient { return &fakeSSHClient{closed: make(chan struct{})} }

func (f *fakeSSHClient) SendRequest(string, bool, []byte) (bool, []byte, error) {
	if f.block {
		<-f.closed
		return false, nil, errors.New("closed")
	}
	return false, nil, f.err
}

func (f *fakeSSHClient) Close() error {
	f.once.Do(func() { close(f.closed) })
	return nil
}

func (f *fakeSSHClient) isClosed() bool {
	select {
	case <-f.closed:
		return true
	default:
		return false
	}
}

// A keepalive that gets no reply, or fails, closes the client.
func TestKeepAliveClosesDeadClient(t *testing.T) {
	for name, c := range map[string]*fakeSSHClient{
		"no reply": {block: true, closed: make(chan struct{})},
		"error":    {err: errors.New("broken pipe"), closed: make(chan struct{})},
	} {
		t.Run(name, func(t *testing.T) {
			done := make(chan struct{})
			defer close(done)
			go keepAlive(c, 10*time.Millisecond, 50*time.Millisecond, done)
			select {
			case <-c.closed:
			case <-time.After(5 * time.Second):
				t.Fatal("client not closed")
			}
		})
	}
}

// A keepalive that is answered, even with a refusal, leaves the client open.
func TestKeepAliveKeepsLiveClient(t *testing.T) {
	c := newFakeSSHClient()
	done := make(chan struct{})
	go keepAlive(c, 10*time.Millisecond, 50*time.Millisecond, done)
	time.Sleep(300 * time.Millisecond)
	close(done)
	if c.isClosed() {
		t.Fatal("a live client was closed")
	}
}
