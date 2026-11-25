package usage

import (
	"syscall"
	"time"
)

// rusage reads the usage of this process and of the children it has reaped.
func rusage() (self, children figures, ok bool) {
	var s, c syscall.Rusage
	if syscall.Getrusage(syscall.RUSAGE_SELF, &s) != nil || syscall.Getrusage(syscall.RUSAGE_CHILDREN, &c) != nil {
		return figures{}, figures{}, false
	}
	return figuresOf(s), figuresOf(c), true
}

// figuresOf is the CPU time and peak memory of one rusage. The peak is in kB.
func figuresOf(r syscall.Rusage) figures {
	return figures{
		cpu:    time.Duration(r.Utime.Nano() + r.Stime.Nano()),
		peakKB: r.Maxrss,
	}
}
