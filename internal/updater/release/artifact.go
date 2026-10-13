package release

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
)

func VerifyArtifact(reader io.Reader, artifact Artifact) error {
	if reader == nil {
		return errors.New("artifact reader is required")
	}
	if err := validateArtifact(artifact); err != nil {
		return err
	}
	hasher := sha256.New()
	written, err := io.Copy(hasher, io.LimitReader(reader, artifact.Size+1))
	if err != nil {
		return fmt.Errorf("read release artifact: %w", err)
	}
	if written != artifact.Size {
		return fmt.Errorf("release artifact size mismatch: got %d, want %d", written, artifact.Size)
	}
	want, _ := hex.DecodeString(artifact.SHA256)
	if subtle.ConstantTimeCompare(hasher.Sum(nil), want) != 1 {
		return errors.New("release artifact digest mismatch")
	}
	return nil
}
