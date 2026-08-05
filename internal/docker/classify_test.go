package docker

import (
	"errors"
	"testing"

	"github.com/fsouza/go-dockerclient"
)

func TestClassifyError(t *testing.T) {
	var nsc = &docker.NoSuchContainer{ID: "abc", Err: errors.New("404")}
	if got := classifyError(nsc); !errors.Is(got, ErrNotFound) {
		t.Errorf("classifyError(NoSuchContainer) = %v, want ErrNotFound", got)
	}
	if got := classifyError(errors.New("boom")); errors.Is(got, ErrNotFound) {
		t.Errorf("classifyError(generic) = %v, want not ErrNotFound", got)
	}
	if got := classifyError(nil); got != nil {
		t.Errorf("classifyError(nil) = %v, want nil", got)
	}
}
