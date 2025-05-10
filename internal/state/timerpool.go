package state

import (
	"sync"
	"time"
)

// This implementation relies on [Go 1.23+ behavior] and is therefore not much
// more than a type-safe wrapper over [sync.Pool].
//
// [Go 1.23+ behavior]: https://pkg.go.dev/time#NewTimer
type TimerPool struct {
	p sync.Pool
}

func (tp *TimerPool) Init() {
	tp.p.New = func() any {
		return time.NewTimer(0)
	}
}

func (tp *TimerPool) Get() *time.Timer {
	return tp.p.Get().(*time.Timer)
}

func (tp *TimerPool) Put(t *time.Timer) {
	tp.p.Put(t)
}
