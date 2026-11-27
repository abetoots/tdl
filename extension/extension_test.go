package extension

import (
	"testing"

	"github.com/gotd/td/telegram"
)

func TestClientOptionsPreserveMiddlewares(t *testing.T) {
	env := &Env{}
	t.Run("nil uses client defaults once", func(t *testing.T) {
		if got := clientOptions(env, Options{}).Middlewares; got != nil {
			t.Errorf("middlewares = %v, want nil", got)
		}
	})
	t.Run("custom middleware passes through", func(t *testing.T) {
		middleware := &struct{ telegram.Middleware }{}
		want := []telegram.Middleware{middleware}
		got := clientOptions(env, Options{Middlewares: want}).Middlewares
		if len(got) != 1 || got[0] != middleware || &got[0] != &want[0] {
			t.Errorf("middlewares = %v, want the original slice %v", got, want)
		}
	})
}
