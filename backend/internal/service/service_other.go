//go:build !linux && !darwin && !windows

package service

import (
	"context"
	"errors"
)

var errUnsupported = errors.New("installing as a service is supported on Linux (systemd), macOS and Windows")

func Default() Paths                                             { return Paths{} }
func Install(Options) (Paths, error)                             { return Paths{}, errUnsupported }
func Uninstall() (Paths, error)                                  { return Paths{}, errUnsupported }
func Start() error                                               { return errUnsupported }
func Stop() error                                                { return errUnsupported }
func Restart() error                                             { return errUnsupported }
func Status() error                                              { return errUnsupported }
func LogsHint() string                                           { return "" }
func RunAsService(func(ctx context.Context) error) (bool, error) { return false, nil }
