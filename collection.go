package qngg

import "context"

// arrayFrom returns Array.from(v), such as the entries of a Map or the
// values of a Set.
func (r *Runtime) arrayFrom(v *Value) (*Value, error) {
	array, err := r.global.Get("Array")
	if err != nil {
		return nil, err
	}
	defer array.Free()

	return array.Invoke(context.Background(), "from", v)
}

// entries calls fn with each key and value of a Map, or with each own
// property of another object (see forEach), until fn returns an error. Both
// values are only valid during the call.
func (v *Value) entries(fn func(key, value *Value) error) error {
	if !v.IsMap() {
		return v.forEach(fn)
	}

	pairs, err := v.rt.arrayFrom(v)
	if err != nil {
		return err
	}
	defer pairs.Free()

	for i := range uint32(pairs.Len()) {
		if err := mapEntry(pairs, i, fn); err != nil {
			return err
		}
	}

	return nil
}

// mapEntry calls fn with the key and value of the [key, value] pair at
// index i of pairs.
func mapEntry(pairs *Value, i uint32, fn func(key, value *Value) error) error {
	pair, err := pairs.Index(i)
	if err != nil {
		return err
	}
	defer pair.Free()

	key, err := pair.Index(0)
	if err != nil {
		return err
	}
	defer key.Free()

	value, err := pair.Index(1)
	if err != nil {
		return err
	}
	defer value.Free()

	return fn(key, value)
}
