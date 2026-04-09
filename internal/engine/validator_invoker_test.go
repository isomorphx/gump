package engine

import (
	"testing"

	"github.com/isomorphx/gump/internal/state"
)

func TestValidatorInvokerImpl_NilSubRunner(t *testing.T) {
	vi := &ValidatorInvokerImpl{ResolveCtx: &state.ResolveContext{}}
	ok, err := vi.InvokeValidator("validators/x", nil)
	if err != nil || ok {
		t.Fatalf("nil subrunner should yield false,nil got ok=%v err=%v", ok, err)
	}
}
