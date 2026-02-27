package bwrap

import "errors"

// ErrNotLinux is returned when bubblewrap is requested on non-Linux systems.
var ErrNotLinux = errors.New("bubblewrap sandboxing is only available on Linux")

// ErrBwrapNotFound is returned when the bwrap binary cannot be located.
var ErrBwrapNotFound = errors.New("bwrap binary not found")
