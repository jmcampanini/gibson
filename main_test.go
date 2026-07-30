package main

import (
	"bytes"
	"errors"
	"testing"

	"github.com/jmcampanini/gibson/internal/app"
	"github.com/stretchr/testify/assert"
)

func TestProcessExitCode(t *testing.T) {
	tests := map[string]struct {
		outcome app.RunOutcome
		err     error
		code    int
		stderr  string
	}{
		"completed": {
			outcome: app.RunCompleted,
			code:    0,
		},
		"failed": {
			outcome: app.RunCompleted,
			err:     errors.New("run failed"),
			code:    1,
			stderr:  "gibson: error: run failed\n",
		},
		"interrupted": {
			outcome: app.RunInterrupted,
			code:    130,
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			var stderr bytes.Buffer
			code := processExitCode(test.outcome, test.err, &stderr)

			assert.Equal(t, test.code, code)
			assert.Equal(t, test.stderr, stderr.String())
		})
	}
}
