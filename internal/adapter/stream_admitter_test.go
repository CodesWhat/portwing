package adapter

import "testing"

// TestStreamAdmitterAdmit covers the nil-is-always-admit contract described
// on StreamAdmitter.Admit: a nil StreamAdmitter must always admit, and a
// non-nil one must delegate to the underlying func exactly, in both the
// admit and refuse cases.
func TestStreamAdmitterAdmit(t *testing.T) {
	t.Run("nil admitter always admits", func(t *testing.T) {
		var f StreamAdmitter

		release, ok := f.Admit()

		if !ok {
			t.Fatal("Admit() on a nil StreamAdmitter returned ok == false, want true")
		}
		if release == nil {
			t.Fatal("Admit() on a nil StreamAdmitter returned a nil release func, want non-nil")
		}

		release() // must not panic
	})

	t.Run("non-nil admitter delegates on admit", func(t *testing.T) {
		var released bool
		want := func() { released = true }

		f := StreamAdmitter(func() (func(), bool) {
			return want, true
		})

		release, ok := f.Admit()

		if !ok {
			t.Fatal("Admit() returned ok == false, want true from underlying func")
		}
		if release == nil {
			t.Fatal("Admit() returned a nil release func, want the underlying func's release")
		}

		release()
		if !released {
			t.Fatal("Admit() did not return the exact release func produced by the underlying admitter")
		}
	})

	t.Run("non-nil admitter delegates on refuse", func(t *testing.T) {
		f := StreamAdmitter(func() (func(), bool) {
			return nil, false
		})

		release, ok := f.Admit()

		if ok {
			t.Fatal("Admit() returned ok == true, want false from underlying func")
		}
		if release != nil {
			t.Fatal("Admit() returned a non-nil release func, want the underlying func's nil release")
		}
	})
}
