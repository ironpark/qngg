#include "qjs.h"
#include <errno.h>
#include <stdio.h>
#include <string.h>
#include <stdlib.h>
#include <stdbool.h>

/* Detect and load source code from a file or memory.
 * For file input, returns an allocated buffer (caller must free).
 * For in-memory input, returns the provided buffer.
 */
static uint8_t *detect_buf(JSContext *ctx, QJSEvalOptions *opts, size_t *buf_len, bool is_file)
{
    // If the input is a file, load the file into a buffer.
    // The buffer will be freed by the caller.
    // buf_len will be set to the length of the buffer.
    if (is_file)
    {
        if (!opts->filename || opts->filename[0] == '\0')
            return NULL;

        opts->filename = detect_entry_point((char *)opts->filename);
        return js_load_file(ctx, buf_len, opts->filename);
    }
    // If the input is in-memory, return the provided buffer
    // and set the buffer length
    if (opts->buf)
    {
        *buf_len = strlen(opts->buf);
        if (!opts->filename || opts->filename[0] == '\0')
            opts->filename = "<input>";

        return (uint8_t *)opts->buf;
    }
    // If the input is bytecode, return the provided buffer
    // and set the buffer length
    if (opts->bytecode_buf)
    {
        *buf_len = opts->bytecode_len;
        return (uint8_t *)opts->bytecode_buf;
    }

    return NULL;
}

/* Returns a thrown exception for an empty buffer.
 * If buf is non-NULL, this function returns JS_NULL.
 */
static JSValue buf_empty_error(JSContext *ctx, bool is_file, QJSEvalOptions *opts)
{
    if (JS_HasException(ctx))
        return JS_Throw(ctx, JS_GetException(ctx));

    if (errno != 0)
        return JS_ThrowReferenceError(ctx, "%s: %s", strerror(errno), opts->filename);

    if (is_file)
    {
        if (!opts->filename || opts->filename[0] == '\0')
            return JS_ThrowReferenceError(ctx, "filename is required for file evaluation");
        else
            return JS_ThrowReferenceError(ctx, "could not load file: %s", opts->filename);
    }

    return JS_ThrowReferenceError(ctx, "in-memory buffer/bytecode is required for evaluation");
}

static JSValue load_buf(JSContext *ctx, QJSEvalOptions opts, int flags, bool eval)
{
    bool is_file = input_is_file(opts);
    size_t buf_len = 0;
    uint8_t *buf = detect_buf(ctx, &opts, &buf_len, is_file);
    if (buf == NULL)
        return buf_empty_error(ctx, is_file, &opts);

    JSValue module_val;
    if (opts.bytecode_buf != NULL)
        if (!eval)
        {
            module_val = JS_ReadObject(ctx, opts.bytecode_buf, opts.bytecode_len, JS_READ_OBJ_BYTECODE);
            if (JS_IsException(module_val))
            {
                if (is_file)
                    js_free(ctx, buf);
                return JS_Throw(ctx, JS_GetException(ctx));
            }
        }
        else
        {
            JSValue obj = JS_ReadObject(ctx, opts.bytecode_buf, opts.bytecode_len, JS_READ_OBJ_BYTECODE);
            if (JS_IsException(obj))
            {
                if (is_file)
                    js_free(ctx, buf);
                return JS_Throw(ctx, JS_GetException(ctx));
            }
            module_val = JS_EvalFunction(ctx, obj);
            if (JS_IsException(module_val))
            {
                JS_FreeValue(ctx, obj);
                if (is_file)
                    js_free(ctx, buf);
                return JS_Throw(ctx, JS_GetException(ctx));
            }
        }

    else
    {
        module_val = JS_Eval(ctx, (const char *)buf, buf_len, opts.filename, flags);
        if (JS_IsException(module_val))
        {
            if (is_file)
                js_free(ctx, buf);
            return JS_Throw(ctx, JS_GetException(ctx));
        }
    }

    if (is_file)
        js_free(ctx, buf);

    return module_val;
}

/* Create a JSON module with the parsed JSON as default export */
static JSModuleDef *js_module_loader_json(JSContext *ctx, const char *module_name)
{
    JSModuleDef *m;
    size_t buf_len;
    uint8_t *buf;
    JSValue json_val, func_val;

    buf = js_load_file(ctx, &buf_len, module_name);
    if (!buf)
    {
        JS_ThrowReferenceError(ctx, "could not load JSON module filename '%s'", module_name);
        return NULL;
    }

    /* Parse the JSON content */
    json_val = JS_ParseJSON(ctx, (char *)buf, buf_len, module_name);
    js_free(ctx, buf);
    if (JS_IsException(json_val))
        return NULL;

    /* Create a synthetic module source that exports the JSON */
    const char *module_source_template =
        "const __json_data__ = %s;\n"
        "export default __json_data__;\n";

    /* Convert JSON to string */
    JSValue json_str = JS_JSONStringify(ctx, json_val, JS_NULL, JS_NULL);
    if (JS_IsException(json_str))
    {
        JS_FreeValue(ctx, json_val);
        return NULL;
    }

    const char *json_string = JS_ToCString(ctx, json_str);
    if (!json_string)
    {
        JS_FreeValue(ctx, json_val);
        JS_FreeValue(ctx, json_str);
        return NULL;
    }

    /* Create the module source */
    size_t source_len = strlen(module_source_template) + strlen(json_string) + 1;
    char *module_source = malloc(source_len);
    if (!module_source)
    {
        JS_FreeCString(ctx, json_string);
        JS_FreeValue(ctx, json_str);
        JS_FreeValue(ctx, json_val);
        return NULL;
    }
    snprintf(module_source, source_len, module_source_template, json_string);

    /* Compile the module */
    func_val = JS_Eval(ctx, module_source, strlen(module_source), module_name,
                       JS_EVAL_TYPE_MODULE | JS_EVAL_FLAG_COMPILE_ONLY);

    /* Cleanup */
    free(module_source);
    JS_FreeCString(ctx, json_string);
    JS_FreeValue(ctx, json_str);
    JS_FreeValue(ctx, json_val);

    if (JS_IsException(func_val))
        return NULL;

    /* Set import.meta */
    if (js_module_set_import_meta(ctx, func_val, true, false) < 0)
    {
        JS_FreeValue(ctx, func_val);
        return NULL;
    }

    /* Return the compiled module */
    m = JS_VALUE_GET_PTR(func_val);
    JS_FreeValue(ctx, func_val);
    return m;
}

/* Module loader with support for appending common suffixes and JSON modules */
JSModuleDef *QJS_ModuleLoader(JSContext *ctx, const char *module_name, void *opaque,
                              JSValueConst attributes)
{
    module_name = detect_entry_point((char *)module_name);
    if (!module_name)
        return NULL;

    /* Check if it's a JSON module */
    if (js__has_suffix(module_name, ".json"))
    {
        return js_module_loader_json(ctx, module_name);
    }

    JSModuleDef *mod = js_module_loader(ctx, module_name, opaque, attributes);
    return mod;
}

/* Loads a module, compiles it (with import.meta support), and resolves it */
JSValue QJS_Load(JSContext *ctx, QJSEvalOptions opts)
{
    if ((opts.eval_flags & JS_EVAL_TYPE_MASK) != JS_EVAL_TYPE_MODULE)
        return JS_ThrowTypeError(ctx, "load only supports module evaluation");

    int eval_flags = (opts.eval_flags ? opts.eval_flags : JS_EVAL_TYPE_MODULE);
    eval_flags |= JS_EVAL_TYPE_MODULE | JS_EVAL_FLAG_COMPILE_ONLY;

    JSValue module_val = load_buf(ctx, opts, eval_flags, false);
    if (JS_IsException(module_val))
        return JS_Throw(ctx, JS_GetException(ctx));

    if (JS_VALUE_GET_TAG(module_val) != JS_TAG_MODULE)
        return JS_ThrowTypeError(ctx, "is not a module");

    if (JS_ResolveModule(ctx, module_val) != 0)
    {
        JSValue ex = JS_HasException(ctx) ? JS_Throw(ctx, JS_GetException(ctx))
                                          : JS_ThrowTypeError(ctx, "resolve module failed");
        JS_FreeValue(ctx, module_val);
        return ex;
    }

    if (js_module_set_import_meta(ctx, module_val, input_is_file(opts), true) < 0)
    {
        JSValue ex = JS_Throw(ctx, JS_GetException(ctx));
        JS_FreeValue(ctx, module_val);
        return ex;
    }

    return module_val;
}

/* Evaluates a module with support for promises and default exports */
static JSValue qjs_eval_module(JSContext *ctx, QJSEvalOptions opts)
{
    JSValue module_val = QJS_Load(ctx, opts);
    if (JS_IsException(module_val))
        return module_val;

    JSValue result = JS_EvalFunction(ctx, module_val);
    if (JS_IsPromise(result))
        result = js_std_await(ctx, result);

    js_std_loop(ctx);
    if (JS_IsException(result))
        return JS_Throw(ctx, JS_GetException(ctx));

    JSModuleDef *module_def = JS_VALUE_GET_PTR(module_val);
    JSValue ns = JS_GetModuleNamespace(ctx, module_def);
    JSAtom default_atom = JS_NewAtom(ctx, "default");
    JSValue default_export = JS_GetProperty(ctx, ns, default_atom);
    JS_FreeAtom(ctx, default_atom);
    JS_FreeValue(ctx, ns);

    if (!JS_IsUndefined(default_export))
    {
        JS_FreeValue(ctx, result);
        return default_export;
    }

    JS_FreeValue(ctx, default_export);
    return result;
}

/* Evaluates code (as a module or global script) with promise handling */
JSValue QJS_Eval(JSContext *ctx, QJSEvalOptions opts)
{
    bool is_module = (opts.eval_flags & JS_EVAL_TYPE_MASK) == JS_EVAL_TYPE_MODULE;
    bool is_compile_only = (opts.eval_flags & JS_EVAL_FLAG_COMPILE_ONLY) > 0;
    JSValue result;

    // compile_only always uses qjs_eval_global
    if (!is_module || is_compile_only)
        result = load_buf(ctx, opts, opts.eval_flags, !is_compile_only);
    else
        result = qjs_eval_module(ctx, opts);

    if (JS_IsException(result))
        return JS_Throw(ctx, JS_GetException(ctx));

    if (JS_IsPromise(result))
        result = js_std_await(ctx, result);

    js_std_loop(ctx);
    if (JS_HasException(ctx))
        return JS_Throw(ctx, JS_GetException(ctx));

    // If the eval is ran in GLOBAL mode with flag JS_EVAL_FLAG_ASYNC
    // The result will be an object of: { value: <result> }
    // We need to extract the value from the object
    bool is_global = (opts.eval_flags & JS_EVAL_TYPE_MASK) == JS_EVAL_TYPE_GLOBAL;
    bool is_async = (opts.eval_flags & JS_EVAL_FLAG_ASYNC) > 0;
    bool is_async_global = is_global && is_async;
    if (is_async_global)
    {
        // JSValue result_json = JS_JSONStringify(ctx, result, JS_NULL, JS_NULL);
        // printf("result is: %s\n", JS_ToCString(ctx, result_json));
        JSAtom value_atom = JS_NewAtom(ctx, "value");
        JSValue value = JS_GetProperty(ctx, result, value_atom);
        JS_FreeAtom(ctx, value_atom);
        JS_FreeValue(ctx, result);
        result = value;
    }

    return result;
}

/*
 * Compiles code (as a module or global script) to bytecode, returned as
 * (ptr << 32 | len). The caller releases the bytecode with js_free. On
 * failure, UINT64_MAX is returned with the exception left pending.
 */
uint64_t QJS_Compile(JSContext *ctx, QJSEvalOptions opts)
{
    opts.eval_flags = opts.eval_flags ? opts.eval_flags : JS_EVAL_TYPE_GLOBAL;
    opts.eval_flags |= JS_EVAL_FLAG_COMPILE_ONLY;

    JSValue val = QJS_Eval(ctx, opts);
    if (JS_IsException(val))
        return UINT64_MAX;

    size_t len = 0;
    uint8_t *bytecode = JS_WriteObject(ctx, &len, val, JS_WRITE_OBJ_BYTECODE);
    JS_FreeValue(ctx, val);
    if (!bytecode)
        return UINT64_MAX;

    return ((uint64_t)(uintptr_t)bytecode << 32) | (uint32_t)len;
}
