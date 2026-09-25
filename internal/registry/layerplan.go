package registry

// How an artifact is cut into layers.
//
//   - The binding limit is a count of requests, not of bytes.
//   - A fresh layer costs three: HEAD for presence, POST to open, PUT to commit.
//   - So the layer count stays roughly constant and the size follows the artifact.
//   - A fixed size would let the count grow with the artifact.
//   - A pull reads boundaries from the manifest, so the size is recorded nowhere.
const (
	// targetLayers stays well under the registry's request limit, which cannot
	// be queried.
	targetLayers = 24
	// minLayerSize is the floor: below it there is nothing left to optimize.
	minLayerSize = 2 << 30
	// layerGranularity keeps sizes to whole GiB so artifacts of similar size
	// share boundaries and deduplicate. A continuous size/targetLayers would give
	// two artifacts one byte apart entirely different boundaries.
	layerGranularity = 1 << 30
)

// layerSizeReason names why a layer size was chosen, so the upload plan can
// report it.
type layerSizeReason string

const (
	layerSizeFloor   layerSizeReason = "floor"
	layerSizeDerived layerSizeReason = "derived"
	layerSizeClamped layerSizeReason = "clamped"
)

// planLayerSize returns the layer size for an artifact of size bytes, and why.
// maxLayerSize is the destination's per-layer limit, or zero when unknown.
//
//   - There is no override and no setting.
//   - It is pure in its arguments, so a resumed push plans the same boundaries.
func planLayerSize(size, maxLayerSize int64) (int64, layerSizeReason) {
	chosen, reason := ceilTo(ceilDiv(size, targetLayers), layerGranularity), layerSizeDerived
	if chosen <= minLayerSize {
		chosen, reason = minLayerSize, layerSizeFloor
	}
	// A hard limit outranks our floor: the registry will refuse what it refuses.
	if maxLayerSize > 0 && chosen > maxLayerSize {
		chosen, reason = floorTo(maxLayerSize, layerGranularity), layerSizeClamped
		if chosen == 0 {
			chosen = maxLayerSize
		}
	}
	return chosen, reason
}

// layerCount reports how many layers an artifact of size bytes cuts into. An
// empty artifact is one empty layer, so every artifact has a payload descriptor
// and pull needs no zero case.
func layerCount(size, layerSize int64) int {
	if size <= 0 || layerSize <= 0 {
		return 1
	}
	return int(ceilDiv(size, layerSize))
}

// ceilDiv divides, rounding up. Saturating rather than overflowing: a+b-1 wraps
// for a size near the int64 maximum, and a wrapped layer count is a plan that
// looks reasonable and is not.
func ceilDiv(a, b int64) int64 {
	if a <= 0 || b <= 0 {
		return 0
	}
	q := a / b
	if a%b != 0 {
		q++
	}
	return q
}

// ceilTo rounds up to a multiple of step, saturating rather than wrapping.
func ceilTo(v, step int64) int64 {
	if v <= 0 || step <= 0 {
		return 0
	}
	n := ceilDiv(v, step)
	if n > (1<<62)/step {
		return v
	}
	return n * step
}

// floorTo rounds down to a multiple of step, which may be zero when v is below
// one step. Callers decide what that means.
func floorTo(v, step int64) int64 {
	if v <= 0 || step <= 0 {
		return 0
	}
	return v / step * step
}
