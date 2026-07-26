package app

import (
	"context"
	"errors"
)

type ServeOptions struct {
	PortOverride *int
	Dev          bool
}

func Serve(context.Context, ServeOptions) error {
	return errors.New("server startup is not available in this build")
}
