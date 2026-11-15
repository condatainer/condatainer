package hostrun

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"strconv"
)

// ErrNoRelay means the container has no relay to ask.
var ErrNoRelay = errors.New("this container has no host relay: start it from a launch that host_run.launch names")

// ErrStdinTooLarge means standard input is over MaxStdin.
var ErrStdinTooLarge = fmt.Errorf("standard input is over %d bytes: write the data to a file on the shared file system and run the command on it", MaxStdin)

// Call is one command a container asks its host to run.
type Call struct {
	Sock   string    // the relay socket
	Argv   []string  // the command and its arguments
	Cwd    string    // the working directory
	On     string    // the node to run on; "" takes the launch's default
	Stdin  io.Reader // standard input to send; nil sends none
	Stdout io.Writer
	Stderr io.Writer
}

// Run sends the command and copies its output until it ends. It returns the command's exit code.
func (c Call) Run(ctx context.Context) (int, error) {
	if c.Sock == "" {
		return 0, ErrNoRelay
	}
	var stdin []byte
	if c.Stdin != nil {
		var err error
		if stdin, err = io.ReadAll(io.LimitReader(c.Stdin, MaxStdin+1)); err != nil {
			return 0, err
		}
		if len(stdin) > MaxStdin {
			return 0, ErrStdinTooLarge
		}
	}

	var dialer net.Dialer
	conn, err := dialer.DialContext(ctx, "unix", c.Sock)
	if err != nil {
		return 0, fmt.Errorf("%w (%v)", ErrNoRelay, err)
	}
	defer conn.Close()
	stop := context.AfterFunc(ctx, func() { conn.Close() })
	defer stop()

	head, err := json.Marshal(request{Argv: c.Argv, Cwd: c.Cwd, On: c.On, StdinLen: len(stdin)})
	if err != nil {
		return 0, err
	}
	if _, err := conn.Write(append(append(head, '\n'), stdin...)); err != nil {
		return 0, err
	}

	for {
		typ, data, err := readFrame(conn)
		if err != nil {
			if ctx.Err() != nil {
				return 0, ctx.Err()
			}
			return 0, errors.New("the relay closed the connection before the command ended")
		}
		switch typ {
		case frameOut:
			c.Stdout.Write(data) //nolint:errcheck
		case frameErr:
			c.Stderr.Write(data) //nolint:errcheck
		case frameExit:
			code, err := strconv.Atoi(string(data))
			if err != nil {
				return 0, fmt.Errorf("the relay sent an exit code that is not a number: %q", data)
			}
			return code, nil
		}
	}
}
