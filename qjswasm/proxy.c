#include "qjs.h"

// Go values referenced from JavaScript: Go functions and the values wrapped by
// proxy values. Both hold the ID of the value in the Go registry, which is
// released when the JavaScript object is finalized.

#ifdef __wasm__
__attribute__((import_module("env"), import_name("jsFunctionProxy"))) extern JSValue jsFunctionProxy(
    JSContext *ctx, JSValueConst this_val, int argc, JSValueConst *argv, uint32_t id, JSValue *resolvers);
__attribute__((import_module("env"), import_name("jsReleaseProxy"))) extern void jsReleaseProxy(uint32_t id);
#else
static JSValue jsFunctionProxy(JSContext *ctx, JSValueConst this_val, int argc, JSValueConst *argv,
                               uint32_t id, JSValue *resolvers)
{
  return JS_ThrowInternalError(ctx, "Go functions are only available in WebAssembly");
}
static void jsReleaseProxy(uint32_t id) {}
#endif

static JSClassID proxy_value_class_id;

static void release_go_ref(void *opaque)
{
  jsReleaseProxy((uint32_t)(uintptr_t)opaque);
}

// Calls the Go function. For async functions (magic != 0), Go settles the
// promise returned to JavaScript through its resolving functions.
static JSValue call_go_function(JSContext *ctx, JSValueConst this_val, int argc, JSValueConst *argv, int magic,
                                void *opaque)
{
  uint32_t id = (uint32_t)(uintptr_t)opaque;
  if (!magic)
    return jsFunctionProxy(ctx, this_val, argc, argv, id, NULL);

  JSValue resolvers[2];
  JSValue promise = JS_NewPromiseCapability(ctx, resolvers);
  if (JS_IsException(promise))
    return promise;

  JSValue ret = jsFunctionProxy(ctx, this_val, argc, argv, id, resolvers);
  JS_FreeValue(ctx, resolvers[0]);
  JS_FreeValue(ctx, resolvers[1]);
  if (JS_IsException(ret))
  {
    JS_FreeValue(ctx, promise);
    return ret;
  }

  JS_FreeValue(ctx, ret);
  return promise;
}

// Returns a function calling the Go function registered as `id`. The ID is
// released when the function is finalized, unless creating it fails.
JSValue QJS_NewGoFunction(JSContext *ctx, uint32_t id, const char *name, int async)
{
  return JS_NewCClosure(ctx, call_go_function, name, release_go_ref, 0, async ? 1 : 0, (void *)(uintptr_t)id);
}

static void proxy_value_finalizer(JSRuntime *rt, JSValueConst val)
{
  release_go_ref(JS_GetOpaque(val, proxy_value_class_id));
}

static JSValue proxy_value_to_string(JSContext *ctx, JSValueConst this_val, int argc, JSValueConst *argv)
{
  uint32_t id = (uint32_t)(uintptr_t)JS_GetOpaque(this_val, proxy_value_class_id);
  char buffer[64];
  snprintf(buffer, sizeof(buffer), "[object QJS_PROXY_VALUE(proxyId: %u)]", id);
  return JS_NewString(ctx, buffer);
}

static const JSClassDef proxy_value_class = {
    .class_name = "QJS_PROXY_VALUE",
    .finalizer = proxy_value_finalizer,
};

static const JSCFunctionListEntry proxy_value_proto_funcs[] = {
    JS_CFUNC_DEF("toString", 0, proxy_value_to_string),
};

// Registers the proxy value class. Proxy values can only be created from Go,
// so JavaScript cannot forge references to Go values.
int init_qjs_proxy_value_class(JSContext *ctx)
{
  JSRuntime *rt = JS_GetRuntime(ctx);
  JS_NewClassID(rt, &proxy_value_class_id);
  if (JS_NewClass(rt, proxy_value_class_id, &proxy_value_class) < 0)
    return -1;

  JSValue proto = JS_NewObject(ctx);
  if (JS_IsException(proto))
    return -1;

  JS_SetPropertyFunctionList(ctx, proto, proxy_value_proto_funcs, countof(proxy_value_proto_funcs));
  JS_SetClassProto(ctx, proxy_value_class_id, proto);
  return 0;
}

// Returns a proxy value for the Go value registered as `id`, released when
// the proxy value is finalized.
JSValue QJS_NewProxyValue(JSContext *ctx, uint32_t id)
{
  JSValue obj = JS_NewObjectClass(ctx, proxy_value_class_id);
  if (JS_IsException(obj))
  {
    release_go_ref((void *)(uintptr_t)id);
    return obj;
  }

  JS_SetOpaque(obj, (void *)(uintptr_t)id);
  return obj;
}

// Returns the ID of the Go value wrapped by a proxy value, or 0 if `val` is
// not a proxy value.
uint32_t QJS_GetProxyID(JSValueConst val)
{
  return (uint32_t)(uintptr_t)JS_GetOpaque(val, proxy_value_class_id);
}

// Kinds of objects, as returned by QJS_GetKind.
enum
{
  QJS_KIND_NONE, // not an object
  QJS_KIND_OBJECT,
  QJS_KIND_ARRAY,
  QJS_KIND_FUNCTION,
  QJS_KIND_CONSTRUCTOR,
  QJS_KIND_ERROR,
  QJS_KIND_DATE,
  QJS_KIND_REGEXP,
  QJS_KIND_MAP,
  QJS_KIND_SET,
  QJS_KIND_PROMISE,
  QJS_KIND_ARRAY_BUFFER,
  QJS_KIND_TYPED_ARRAY,
  QJS_KIND_DATA_VIEW,
  QJS_KIND_PROXY_VALUE,
};

// Classifies a value by its class, which unlike `instanceof` cannot be
// changed or spoofed by JavaScript.
int QJS_GetKind(JSContext *ctx, JSValueConst v)
{
  if (!JS_IsObject(v))
    return QJS_KIND_NONE;
  if (JS_GetClassID(v) == proxy_value_class_id)
    return QJS_KIND_PROXY_VALUE;
  if (JS_IsArray(v))
    return QJS_KIND_ARRAY;
  if (JS_IsError(v))
    return QJS_KIND_ERROR;
  if (JS_IsDate(v))
    return QJS_KIND_DATE;
  if (JS_IsRegExp(v))
    return QJS_KIND_REGEXP;
  if (JS_IsMap(v))
    return QJS_KIND_MAP;
  if (JS_IsSet(v))
    return QJS_KIND_SET;
  if (JS_IsArrayBuffer(v))
    return QJS_KIND_ARRAY_BUFFER;
  if (JS_GetTypedArrayType(v) >= 0)
    return QJS_KIND_TYPED_ARRAY;
  if (JS_IsDataView(v))
    return QJS_KIND_DATA_VIEW;
  if (JS_IsPromise(v))
    return QJS_KIND_PROMISE;
  if (JS_IsConstructor(ctx, v))
    return QJS_KIND_CONSTRUCTOR;
  if (JS_IsFunction(ctx, v))
    return QJS_KIND_FUNCTION;
  return QJS_KIND_OBJECT;
}
