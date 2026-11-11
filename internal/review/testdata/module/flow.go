// Package fixture is a small review target.
package fixture

import "example.test/fixture/helper"

// Runner dispatches indirectly.
type Runner interface{ Run() }

// Thing is concrete.
type Thing struct{ Value int }

// Run implements Runner.
func (t Thing) Run() { helper.Help() }

// Flow has seven decisions plus its entry path.
func Flow(n int, fn func(), r Runner) int {
	// <script>alert("source")</script>
	if n > 0 && n < 10 {
		n++
	}
	for i := 0; i < n; i++ {
		helper.Help()
	}
	switch n {
	case 1, 2:
		n++
	case 3:
		n--
	default:
		n = 0
	}
	select {
	case <-make(chan int):
		n++
	default:
	}
	if n == 0 || n == 1 {
		n++
	}
	fn()
	r.Run()
	Thing{}.Run()
	helper.Help()
	func() {
		if n > 1 {
			helper.Help()
		}
	}()
	return n
}

// AliasCall exercises a local function value, not a direct static call.
func AliasCall() { f := helper.Help; f() }

// Generic returns its argument.
func Generic[T any](v T) T { return v }

func UseGeneric() { _ = Generic[int](3) }

func Nested()         { helper.Help() }
func Indirect()       { f := Nested; f() }
func PanicRecovered() { defer func() { _ = recover() }(); panic("expected") }
func Async()          { done := make(chan bool); go func() { Nested(); done <- true }(); <-done }
