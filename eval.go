package qngg

func load(c *Runtime, file string, flags ...EvalOption) (_ *Value, err error) {
	defer recoverInterrupt(c, &err)

	if file == "" {
		return nil, ErrInvalidFileName
	}

	// Module: Force TypeModule() since load only works with modules
	flags = append(flags, TypeModule())
	option := createEvalOption(c, file, flags...)

	evalOptions := option.Handle()

	defer option.Free()

	result := c.jsCall("QJS_Load", c.ctxPtr(), evalOptions)

	return normalizeJsValue(c, result)
}

func eval(c *Runtime, file string, flags ...EvalOption) (_ *Value, err error) {
	defer recoverInterrupt(c, &err)

	if file == "" {
		return nil, ErrInvalidFileName
	}

	option := createEvalOption(c, file, flags...)

	evalOptions := option.Handle()
	defer option.Free()

	result := c.jsCall("QJS_Eval", c.ctxPtr(), evalOptions)

	return normalizeJsValue(c, result)
}

func compile(c *Runtime, file string, flags ...EvalOption) (_ []byte, err error) {
	defer recoverInterrupt(c, &err)

	option := createEvalOption(c, file, flags...)

	evalOptions := option.Handle()
	defer option.Free()

	result := c.jsCall("QJS_Compile2", c.ctxPtr(), evalOptions)
	if result, err = normalizeJsValue(c, result); err != nil {
		return nil, err
	}

	defer result.handle.Free()

	// Bytes returns a copy and frees the C buffer.
	return result.takeBytes(), nil
}

func normalizeJsValue(c *Runtime, value *Value) (*Value, error) {
	hasException := c.hasException()
	if hasException {
		value.Free()

		return nil, c.exception()
	}

	if value.IsError() {
		defer value.Free()

		return nil, value.exception()
	}

	return value, nil
}

// Load loads a JavaScript module so that later code can import it.
func (r *Runtime) Load(file string, flags ...EvalOption) (*Value, error) {
	return load(r, file, flags...)
}

// Eval evaluates a script in the runtime's context.
func (r *Runtime) Eval(file string, flags ...EvalOption) (*Value, error) {
	return eval(r, file, flags...)
}

// Compile compiles a script into bytecode without running it.
func (r *Runtime) Compile(file string, flags ...EvalOption) ([]byte, error) {
	return compile(r, file, flags...)
}
