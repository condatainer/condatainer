package hostrun

import (
	"encoding/binary"
	"fmt"
	"io"

	"github.com/condatainer/condatainer/internal/config"
)

// Names shared by the host side and the container side.
const (
	// EnvSock holds the relay's socket path inside the container.
	EnvSock = "CNT_HOST_RUN_SOCK"
	// ContainerDir is where a launch's relay directory is bound inside the container.
	ContainerDir = config.ContainerRoot + "/host"
	// ContainerSock is the socket inside the container.
	ContainerSock = ContainerDir + "/sock"
)

const (
	// MaxStdin is the most standard input one request carries.
	MaxStdin = 1 << 20
	// maxRequestLine bounds the request line, which holds the arguments.
	maxRequestLine = 256 << 10
	// maxFrame bounds one reply frame; senders stay at chunkSize.
	maxFrame  = 1 << 20
	chunkSize = 32 << 10
)

// request is what the container side sends: one JSON line, then StdinLen bytes of standard input.
type request struct {
	Argv     []string `json:"argv"`
	Cwd      string   `json:"cwd"`
	On       string   `json:"on,omitempty"`
	StdinLen int      `json:"stdin_len"`
}

// Reply frame types: output on the two streams, then the exit code last.
const (
	frameOut  byte = 'o'
	frameErr  byte = 'e'
	frameExit byte = 'x'
)

// writeFrame writes one frame, a type byte and a length-prefixed payload, with a single Write.
func writeFrame(w io.Writer, typ byte, data []byte) error {
	buf := make([]byte, 5, 5+len(data))
	buf[0] = typ
	binary.BigEndian.PutUint32(buf[1:], uint32(len(data)))
	_, err := w.Write(append(buf, data...))
	return err
}

// readFrame reads the next frame.
func readFrame(r io.Reader) (typ byte, data []byte, err error) {
	var head [5]byte
	if _, err = io.ReadFull(r, head[:]); err != nil {
		return 0, nil, err
	}
	n := binary.BigEndian.Uint32(head[1:])
	if n > maxFrame {
		return 0, nil, fmt.Errorf("host-run: a reply frame of %d bytes is over the limit", n)
	}
	data = make([]byte, n)
	if _, err = io.ReadFull(r, data); err != nil {
		return 0, nil, err
	}
	return head[0], data, nil
}
