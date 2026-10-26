#include "./quickjs/quickjs.h"
#include "./quickjs/quickjs-libc.h"
#include "./quickjs/cutils.h"
#include <time.h>

JSValue JS_GetException(JSContext *ctx);
int JS_DeletePropertyInt64(JSContext *ctx, JSValueConst obj, int64_t idx, int flags);

/* Minimum and maximum values a `signed long int' can hold. (Same as `int').  */
#undef LONG_MIN
#define LONG_MIN (-LONG_MAX - 1L)
#undef LONG_MAX
#define LONG_MAX __LONG_MAX__

#ifndef QJS_NATIVE_MODULE_SUFFIX
#ifdef _WIN32
#define QJS_NATIVE_MODULE_SUFFIX ".dll"
#else
#define QJS_NATIVE_MODULE_SUFFIX ".so"
#endif
#endif

typedef struct QJSEvalOptions
{
  const void *buf;
  const uint8_t *bytecode_buf;
  size_t bytecode_len;
  const char *filename;
  int eval_flags;
} QJSEvalOptions;

typedef struct QJSRuntime
{
  JSRuntime *runtime;
  JSContext *context;
} QJSRuntime;

bool file_exists(const char *path);
void remove_trailing_slashes(char *path);
int js__has_suffix(const char *str, const char *suffix);
char *append_suffix(const char *base, const char *suffix);
bool input_is_file(QJSEvalOptions opts);
void js_set_global_objs(JSContext *ctx);
char *detect_entry_point(char *module_name);
JSValue js_std_await(JSContext *ctx, JSValue obj);
void QJS_Free(QJSRuntime *qjs);
void QJS_FreeValue(JSContext *ctx, JSValue val);
JSValue QJS_CloneValue(JSContext *ctx, JSValue val);
JSContext *QJS_GetContext(QJSRuntime *qjs);
JSRuntime *JS_GetRuntime(JSContext *ctx);

#ifdef QJS_DEBUG_RUNTIME_ADDRESS
void randomize_address_space(void);
#endif

int init_qjs_proxy_value_class(JSContext *ctx);

JSContext *New_QJSContext(JSRuntime *rt);
// QJSRuntime *New_QJS(QJSRuntimeOptions);
QJSRuntime *New_QJS(
    size_t memory_limit,
    size_t max_stack_size,
    size_t gc_threshold);
JSModuleDef *QJS_ModuleLoader(JSContext *ctx, const char *module_name, void *opaque,
                              JSValueConst attributes);
JSValue QJS_Load(JSContext *ctx, QJSEvalOptions opts);
JSValue QJS_Eval(JSContext *ctx, QJSEvalOptions opts);
uint64_t QJS_Compile(JSContext *ctx, QJSEvalOptions opts);

uint64_t QJS_GetArrayBuffer(JSContext *ctx, JSValueConst obj);
void QJS_FreeAtoms(JSContext *ctx, const uint32_t *atoms, uint32_t len);
uint64_t QJS_ToStringBuf(JSContext *ctx, JSValueConst val, char *buf, uint32_t cap);
uint64_t QJS_AtomToStringBuf(JSContext *ctx, JSAtom atom, char *buf, uint32_t cap);
uint64_t QJS_JSONStringifyBuf(JSContext *ctx, JSValueConst val, char *buf, uint32_t cap);
uint64_t QJS_GetOwnPropertyAtoms(JSContext *ctx, JSValueConst obj, int flags, uint32_t *buf, uint32_t cap);
JSValue QJS_GetPropertyLen(JSContext *ctx, JSValueConst obj, const char *name, size_t len);
int QJS_SetPropertyLen(JSContext *ctx, JSValueConst obj, const char *name, size_t len, JSValue val, int dup);
int QJS_HasPropertyLen(JSContext *ctx, JSValueConst obj, const char *name, size_t len);
int QJS_DeletePropertyLen(JSContext *ctx, JSValueConst obj, const char *name, size_t len);
JSValue QJS_GetPropertyValue(JSContext *ctx, JSValueConst obj, JSValueConst key);
int QJS_SetPropertyValue(JSContext *ctx, JSValueConst obj, JSValueConst key, JSValueConst val);
int QJS_SetPropertyUint32(JSContext *ctx, JSValueConst obj, uint32_t idx, JSValue val, int dup);
int64_t QJS_ToInt64(JSContext *ctx, JSValueConst val);
int32_t QJS_ToInt32(JSContext *ctx, JSValueConst val);
uint32_t QJS_ToUint32(JSContext *ctx, JSValueConst val);
double QJS_ToFloat64(JSContext *ctx, JSValueConst val);
double QJS_ToEpochTime(JSContext *ctx, JSValueConst val);
JSValue QJS_ParseJSON(JSContext *ctx, const char *buf);
JSValue QJS_NewBigIntDecimal(JSContext *ctx, const char *digits, size_t len);
JSValue QJS_NewGlobal(JSContext *ctx, const char *name, int argc, JSValueConst *argv);

JSValue QJS_NewGoFunction(JSContext *ctx, uint32_t id, const char *name, int async);
JSValue QJS_NewProxyValue(JSContext *ctx, uint32_t id);
uint32_t QJS_GetProxyID(JSValueConst val);
int QJS_GetKind(JSContext *ctx, JSValueConst v);

