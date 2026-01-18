package go_func_utils

import "runtime/debug"
import "log"

func SafeGo(logger *log.Logger, fn func()) {
	// because there's lots of goroutines and the curses UI swallows up errors to stdout
	// capture the error in our logger before crashing out again...
	go func() {
		defer func() {
			if r := recover(); r != nil {
				logger.Printf("PANIC: %v\n%s", r, debug.Stack())
				panic(r)
			}
		}()
		fn()
	}()
}
