#include "qjs.h"
#include <sys/stat.h>
#include <limits.h>
#include <float.h>
#include <math.h>

#ifdef QJS_DEBUG_RUNTIME_ADDRESS
/**
 * Allocates a random-sized block of memory to randomize address space layout.
 * This helps in debugging by ensuring runtime objects are allocated at different
 * addresses on each run, making pointer-related bugs more apparent.
 */
void randomize_address_space(void)
{
    static unsigned int call_counter = 0;
    int stack_variable;

    // Generate entropy from multiple sources for better randomization
    unsigned int entropy = (unsigned int)((uintptr_t)&stack_variable ^ // Stack address (varies per call)
                                          (uintptr_t)time(NULL) ^      // Current time
                                          (uintptr_t)clock() ^         // Clock ticks (higher resolution)
                                          (++call_counter)             // Incremental counter
    );

    // Allocate 1-1024 bytes to perturb the address space
    size_t allocation_size = (entropy % 1024) + 1;
    volatile void *random_allocation = malloc(allocation_size);

    // Note: Intentionally not freeing to affect subsequent allocations
    // Touch the allocated memory to ensure it's not optimized away
    if (random_allocation)
    {
        *((volatile char *)random_allocation) = 0;
    }
}
#endif

// Copied from "quickjs/qjs.c"
#ifndef countof
#define countof(x) (sizeof(x) / sizeof((x)[0]))
#ifndef endof
#define endof(x) ((x) + countof(x))
#endif
#endif

static JSValue js_gc(
    JSContext *ctx,
    JSValue this_val,
    int argc,
    JSValue *argv)
{
    JS_RunGC(JS_GetRuntime(ctx));
    return JS_UNDEFINED;
}

static JSValue js_navigator_get_userAgent(JSContext *ctx, JSValue this_val)
{
    char version[32];
    snprintf(version, sizeof(version), "quickjs-ng/%s", JS_GetVersion());
    return JS_NewString(ctx, version);
}

static const JSCFunctionListEntry global_obj[] = {
    JS_CFUNC_DEF("gc", 0, js_gc),
};

static const JSCFunctionListEntry navigator_proto_funcs[] = {
    JS_CGETSET_DEF2("userAgent", js_navigator_get_userAgent, NULL, JS_PROP_CONFIGURABLE | JS_PROP_ENUMERABLE),
    JS_PROP_STRING_DEF("[Symbol.toStringTag]", "Navigator", JS_PROP_CONFIGURABLE),
};

void js_set_global_objs(JSContext *ctx)
{
    JSValue global = JS_GetGlobalObject(ctx);
    JS_SetPropertyFunctionList(
        ctx,
        global,
        global_obj,
        countof(global_obj));
    JSValue navigator_proto = JS_NewObject(ctx);
    JS_SetPropertyFunctionList(
        ctx,
        navigator_proto,
        navigator_proto_funcs,
        countof(navigator_proto_funcs));
    JSValue navigator = JS_NewObjectProto(ctx, navigator_proto);
    JS_DefinePropertyValueStr(
        ctx,
        global,
        "navigator",
        navigator,
        JS_PROP_CONFIGURABLE | JS_PROP_ENUMERABLE);

    JS_FreeValue(ctx, global);
    JS_FreeValue(ctx, navigator_proto);

    js_std_add_helpers(ctx, 0, 0);

    /* make 'std' and 'os' visible to non module code */
    const char *str =
        "import * as bjson from 'qjs:bjson';\n"
        "import * as std from 'qjs:std';\n"
        "import * as os from 'qjs:os';\n"
        "globalThis.bjson = bjson;\n"
        "globalThis.std = std;\n"
        "globalThis.os = os;\n"
        "globalThis.setTimeout = os.setTimeout;\n"
        "globalThis.setInterval = os.setInterval;\n"
        "globalThis.clearTimeout = os.clearTimeout;\n"
        "globalThis.clearInterval = os.clearInterval;\n";

    QJSEvalOptions opts = {
        .buf = str,
        .filename = "<init_global>",
        .eval_flags = JS_EVAL_TYPE_MODULE};

    JSValue val = QJS_Eval(ctx, opts);
    if (JS_IsException(val))
    {
        js_std_dump_error(ctx);
        exit(1);
    }
    JS_FreeValue(ctx, val);
}

bool file_exists(const char *path)
{
    struct stat path_stat;
    // Perform a stat() system call to get information about the path
    if (stat(path, &path_stat) != 0)
    {
        // If stat() returns a non-zero value, an error occurred (e.g., the path doesn't exist)
        return false;
    }
    // Use the S_ISREG macro to check if the path is a regular file
    return S_ISREG(path_stat.st_mode);
}

// Remove trailing slashes from a given path
void remove_trailing_slashes(char *path)
{
    size_t len = strlen(path);
    while (len > 0 && path[len - 1] == '/')
    {
        path[--len] = '\0';
    }
}

/* Allocate a new string by appending `suffix` to `base` */
char *append_suffix(const char *base, const char *suffix)
{
    size_t base_len = strlen(base);
    size_t suffix_len = strlen(suffix);
    char *result = malloc(base_len + suffix_len + 1);
    if (result)
    {
        strcpy(result, base);
        strcat(result, suffix);
    }
    return result;
}

bool input_is_file(QJSEvalOptions opts)
{
    bool has_bytecode = opts.bytecode_buf != NULL;
    bool has_script = opts.buf != NULL && strlen(opts.buf) > 0;
    return !has_bytecode && !has_script;
}

char *detect_entry_point(char *module_name)
{
    char *module_path = strdup(module_name);
    if (!module_path)
        return NULL;

    remove_trailing_slashes(module_path);

    if (file_exists(module_path))
        return module_path;

    const char *suffixes[] = {".js", ".mjs", "/index.js", "/index.mjs"};
    size_t num_suffixes = sizeof(suffixes) / sizeof(suffixes[0]);

    for (size_t i = 0; i < num_suffixes; i++)
    {
        char *candidate = append_suffix(module_path, suffixes[i]);
        if (!candidate)
            continue; // Allocation failure: skip to next suffix
        if (file_exists(candidate))
        {
            free(module_path);
            return candidate;
        }
    }
    free(module_path);

    return module_name;
}

// Results that may not fit in the caller's buffer `buf` of `cap` bytes are
// returned as (ptr << 32 | len): ptr is 0 if the data was copied into buf,
// and otherwise points to memory the caller releases as documented. Failures
// return UINT64_MAX, with the exception left pending.

// Copies data into buf if it fits, or else returns it by pointer.
static uint64_t out_bytes(const void *data, size_t len, void *buf, uint32_t cap)
{
    if (len <= cap)
    {
        memcpy(buf, data, len);
        return len;
    }

    return ((uint64_t)(uintptr_t)data << 32) | (uint32_t)len;
}

static uint64_t out_cstring(JSContext *ctx, const char *str, size_t len, char *buf, uint32_t cap)
{
    if (!str)
        return UINT64_MAX;

    uint64_t res = out_bytes(str, len, buf, cap);
    if (res == len)
        JS_FreeCString(ctx, str);

    return res;
}

// Converts a value to a string like String(val). A string returned by
// pointer is released with JS_FreeCString.
uint64_t QJS_ToStringBuf(JSContext *ctx, JSValueConst val, char *buf, uint32_t cap)
{
    size_t len;
    const char *str = JS_ToCStringLen(ctx, &len, val);
    return out_cstring(ctx, str, len, buf, cap);
}

// Returns the name of an atom. A string returned by pointer is released
// with JS_FreeCString.
uint64_t QJS_AtomToStringBuf(JSContext *ctx, JSAtom atom, char *buf, uint32_t cap)
{
    size_t len;
    const char *str = JS_AtomToCStringLen(ctx, &len, atom);
    return out_cstring(ctx, str, len, buf, cap);
}

// Returns JSON.stringify(val), or "undefined" for values JSON cannot
// represent. A string returned by pointer is released with JS_FreeCString.
uint64_t QJS_JSONStringifyBuf(JSContext *ctx, JSValueConst val, char *buf, uint32_t cap)
{
    JSValue json = JS_JSONStringify(ctx, val, JS_NULL, JS_NULL);
    if (JS_IsException(json))
        return UINT64_MAX;

    size_t len;
    const char *str = JS_ToCStringLen(ctx, &len, json);
    JS_FreeValue(ctx, json);
    return out_cstring(ctx, str, len, buf, cap);
}

// Returns the contents of an ArrayBuffer as (ptr << 32 | len), valid until
// JavaScript runs again, or UINT64_MAX with the exception left pending.
uint64_t QJS_GetArrayBuffer(JSContext *ctx, JSValueConst obj)
{
    size_t len = 0;
    uint8_t *data = JS_GetArrayBuffer(ctx, &len, obj);
    if (!data)
        return UINT64_MAX;

    return ((uint64_t)(uintptr_t)data << 32) | (uint32_t)len;
}

// QJS_GPN_SKIP_LENGTH makes QJS_GetOwnPropertyAtoms leave out `length`.
#define QJS_GPN_SKIP_LENGTH (1 << 8)

// Returns the atoms of the own properties of obj selected by the JS_GPN_*
// flags and QJS_GPN_SKIP_LENGTH, as uint32 values, with lengths in bytes.
// The caller frees the atoms with QJS_FreeAtoms, and an array returned by
// pointer with free.
uint64_t QJS_GetOwnPropertyAtoms(JSContext *ctx, JSValueConst obj, int flags, uint32_t *buf, uint32_t cap)
{
    cap /= sizeof(uint32_t);

    JSPropertyEnum *tab;
    uint32_t len;
    if (JS_GetOwnPropertyNames(ctx, &tab, &len, obj, flags & ~QJS_GPN_SKIP_LENGTH) < 0)
        return UINT64_MAX;

    uint32_t *atoms = buf;
    if (len > cap)
    {
        atoms = malloc(len * sizeof(uint32_t));
        if (!atoms)
        {
            JS_FreePropertyEnum(ctx, tab, len);
            JS_ThrowOutOfMemory(ctx);
            return UINT64_MAX;
        }
    }

    JSAtom skip = JS_ATOM_NULL;
    if (flags & QJS_GPN_SKIP_LENGTH)
        skip = JS_NewAtom(ctx, "length");

    uint32_t n = 0;
    for (uint32_t i = 0; i < len; i++)
    {
        if (tab[i].atom == skip)
            JS_FreeAtom(ctx, tab[i].atom);
        else
            atoms[n++] = tab[i].atom;
    }

    if (skip != JS_ATOM_NULL)
        JS_FreeAtom(ctx, skip);

    // The atoms now belong to the caller. The array comes from the QuickJS
    // allocator, so it must not be released with free.
    js_free(ctx, tab);

    uint32_t size = n * sizeof(uint32_t);
    return atoms == buf ? size : ((uint64_t)(uintptr_t)atoms << 32) | size;
}

// Frees atoms returned by QJS_GetOwnPropertyAtoms.
void QJS_FreeAtoms(JSContext *ctx, const uint32_t *atoms, uint32_t len)
{
    for (uint32_t i = 0; i < len; i++)
        JS_FreeAtom(ctx, atoms[i]);
}

// Property access by name. Names are UTF-8 of the given length and need no
// NUL terminator. Setters take over val, after duplicating it if dup is set.

JSValue QJS_GetPropertyLen(JSContext *ctx, JSValueConst obj, const char *name, size_t len)
{
    JSAtom atom = JS_NewAtomLen(ctx, name, len);
    if (atom == JS_ATOM_NULL)
        return JS_EXCEPTION;

    JSValue val = JS_GetProperty(ctx, obj, atom);
    JS_FreeAtom(ctx, atom);
    return val;
}

int QJS_SetPropertyLen(JSContext *ctx, JSValueConst obj, const char *name, size_t len, JSValue val, int dup)
{
    if (dup)
        val = JS_DupValue(ctx, val);

    JSAtom atom = JS_NewAtomLen(ctx, name, len);
    if (atom == JS_ATOM_NULL)
    {
        JS_FreeValue(ctx, val);
        return -1;
    }

    int ret = JS_SetProperty(ctx, obj, atom, val);
    JS_FreeAtom(ctx, atom);
    return ret;
}

int QJS_HasPropertyLen(JSContext *ctx, JSValueConst obj, const char *name, size_t len)
{
    JSAtom atom = JS_NewAtomLen(ctx, name, len);
    if (atom == JS_ATOM_NULL)
        return -1;

    int ret = JS_HasProperty(ctx, obj, atom);
    JS_FreeAtom(ctx, atom);
    return ret;
}

int QJS_DeletePropertyLen(JSContext *ctx, JSValueConst obj, const char *name, size_t len)
{
    JSAtom atom = JS_NewAtomLen(ctx, name, len);
    if (atom == JS_ATOM_NULL)
        return -1;

    int ret = JS_DeleteProperty(ctx, obj, atom, 0);
    JS_FreeAtom(ctx, atom);
    return ret;
}

// Property access by key value, such as a symbol. The setter does not take
// over val.

JSValue QJS_GetPropertyValue(JSContext *ctx, JSValueConst obj, JSValueConst key)
{
    JSAtom atom = JS_ValueToAtom(ctx, key);
    if (atom == JS_ATOM_NULL)
        return JS_EXCEPTION;

    JSValue val = JS_GetProperty(ctx, obj, atom);
    JS_FreeAtom(ctx, atom);
    return val;
}

int QJS_SetPropertyValue(JSContext *ctx, JSValueConst obj, JSValueConst key, JSValueConst val)
{
    JSAtom atom = JS_ValueToAtom(ctx, key);
    if (atom == JS_ATOM_NULL)
        return -1;

    int ret = JS_SetProperty(ctx, obj, atom, JS_DupValue(ctx, val));
    JS_FreeAtom(ctx, atom);
    return ret;
}

int QJS_SetPropertyUint32(JSContext *ctx, JSValueConst obj, uint32_t idx, JSValue val, int dup)
{
    if (dup)
        val = JS_DupValue(ctx, val);

    return JS_SetPropertyUint32(ctx, obj, idx, val);
}

// Conversions. They return 0 when the conversion throws, leaving the
// exception pending.

int64_t QJS_ToInt64(JSContext *ctx, JSValueConst val)
{
    int64_t i64;
    return JS_ToInt64(ctx, &i64, val) ? 0 : i64;
}

int32_t QJS_ToInt32(JSContext *ctx, JSValueConst val)
{
    int32_t i32;
    return JS_ToInt32(ctx, &i32, val) ? 0 : i32;
}

uint32_t QJS_ToUint32(JSContext *ctx, JSValueConst val)
{
    uint32_t u32;
    return JS_ToUint32(ctx, &u32, val) ? 0 : u32;
}

double QJS_ToFloat64(JSContext *ctx, JSValueConst val)
{
    double d;
    return JS_ToFloat64(ctx, &d, val) ? 0.0 : d;
}

// Returns the time of a Date in milliseconds since the epoch, or NaN.
double QJS_ToEpochTime(JSContext *ctx, JSValueConst val)
{
    JSAtom get_time = JS_NewAtom(ctx, "getTime");
    JSValue time = JS_Invoke(ctx, val, get_time, 0, NULL);
    JS_FreeAtom(ctx, get_time);
    double ms = NAN;
    if (JS_IsException(time) || JS_ToFloat64(ctx, &ms, time))
        JS_FreeValue(ctx, JS_GetException(ctx));

    JS_FreeValue(ctx, time);
    return ms;
}

JSValue QJS_ParseJSON(JSContext *ctx, const char *buf)
{
    return JS_ParseJSON(ctx, buf, strlen(buf), "<parse_json>");
}

// Returns the BigInt written in decimal in `digits`, with an optional leading
// minus sign. QuickJS has no API for BigInts beyond 64 bits, so this
// evaluates the literal, which cannot run anything else once validated.
JSValue QJS_NewBigIntDecimal(JSContext *ctx, const char *digits, size_t len)
{
    size_t start = len > 0 && digits[0] == '-';
    if (start == len)
        return JS_ThrowSyntaxError(ctx, "invalid BigInt");
    for (size_t i = start; i < len; i++)
        if (digits[i] < '0' || digits[i] > '9')
            return JS_ThrowSyntaxError(ctx, "invalid BigInt");

    char *src = js_malloc(ctx, len + 2);
    if (!src)
        return JS_EXCEPTION;
    memcpy(src, digits, len);
    src[len] = 'n';
    src[len + 1] = '\0';

    JSValue v = JS_Eval(ctx, src, len + 1, "<bigint>", JS_EVAL_TYPE_GLOBAL);
    js_free(ctx, src);
    return v;
}

// Returns `new globalThis[name](...argv)`, or undefined if there is no such
// constructor.
JSValue QJS_NewGlobal(JSContext *ctx, const char *name, int argc, JSValueConst *argv)
{
    JSValue global = JS_GetGlobalObject(ctx);
    JSValue ctor = JS_GetPropertyStr(ctx, global, name);
    JS_FreeValue(ctx, global);
    if (JS_IsException(ctor))
        return ctor;

    JSValue obj = JS_UNDEFINED;
    if (JS_IsConstructor(ctx, ctor))
        obj = JS_CallConstructor(ctx, ctor, argc, argv);

    JS_FreeValue(ctx, ctor);
    return obj;
}
