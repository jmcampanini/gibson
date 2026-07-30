package store

import (
	"crypto/rand"
	"fmt"
	"io"
	"time"
)

const idAlphabet = "abcdefghijklmnopqrstuvwxyz0123456789"

func (s *Store) NewSessionID() (string, error) {
	return newSessionID(time.Now().UTC(), rand.Reader)
}

func newSessionID(now time.Time, random io.Reader) (string, error) {
	const suffixLength = 6
	const unbiasedLimit = 252

	suffix := make([]byte, suffixLength)
	buffer := make([]byte, 1)
	for i := range suffix {
		for {
			if _, err := io.ReadFull(random, buffer); err != nil {
				return "", fmt.Errorf("generate session id: %w", err)
			}
			if int(buffer[0]) >= unbiasedLimit {
				continue
			}
			suffix[i] = idAlphabet[int(buffer[0])%len(idAlphabet)]
			break
		}
	}

	return "s-" + now.UTC().Format("20060102") + "-" + string(suffix), nil
}
