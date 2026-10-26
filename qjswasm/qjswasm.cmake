macro(add_qjs_libc_if_needed target)
    if(NOT QJS_BUILD_LIBC)
        target_sources(${target} PRIVATE quickjs-libc.c)
    endif()
endmacro()
macro(add_static_if_needed target)
    if(QJS_BUILD_CLI_STATIC OR MINGW)
        target_link_options(${target} PRIVATE -static)
        if(MINGW)
            target_link_options(${target} PRIVATE -static-libgcc)
        endif()
    endif()
endmacro()

if(CMAKE_SYSTEM_NAME STREQUAL "WASI")
    add_compile_definitions(
        _WASI_EMULATED_PROCESS_CLOCKS
        _WASI_EMULATED_SIGNAL
    )
    add_link_options(
        -lwasi-emulated-process-clocks
        -lwasi-emulated-signal
    )
endif()

# Optional debug flag for runtime address randomization
option(QJS_DEBUG_RUNTIME_ADDRESS "Enable runtime address randomization debugging" OFF)
if(QJS_DEBUG_RUNTIME_ADDRESS)
    add_compile_definitions(QJS_DEBUG_RUNTIME_ADDRESS)
endif()

if(NOT CMAKE_SYSTEM_NAME STREQUAL "WASI")
    list(APPEND qjs_libs ${CMAKE_THREAD_LIBS_INIT})
endif()

add_executable(qjswasm
    # gen/repl.c
    # gen/standalone.c
    ../eval.c
    ../helpers.c
    ../proxy.c
    ../qjs.c
)

add_qjs_libc_if_needed(qjswasm)
add_static_if_needed(qjswasm)

set_target_properties(qjswasm PROPERTIES
    OUTPUT_NAME "qjswasm"
)

target_link_options(qjswasm PRIVATE
    "LINKER:--export=malloc"
    "LINKER:--export=free"
    "LINKER:--export=js_std_await"
    "LINKER:--export=New_QJS"
    "LINKER:--export=QJS_Free"
    "LINKER:--export=QJS_GetContext"
    "LINKER:--export=QJS_FreeValue"
    "LINKER:--export=QJS_CloneValue"
    "LINKER:--export=JS_GetGlobalObject"
    "LINKER:--export=JS_GetException"
    "LINKER:--export=JS_HasException"
    "LINKER:--export=JS_Throw"
    "LINKER:--export=JS_FreeAtom"
    "LINKER:--export=JS_AtomToValue"
    "LINKER:--export=JS_FreeCString"
    "LINKER:--export=JS_GetProperty"
    "LINKER:--export=JS_ToBool"
    "LINKER:--export=QJS_Eval"
    "LINKER:--export=QJS_Load"
    "LINKER:--export=QJS_Compile"
    "LINKER:--export=QJS_ToStringBuf"
    "LINKER:--export=QJS_AtomToStringBuf"
    "LINKER:--export=QJS_JSONStringifyBuf"
    "LINKER:--export=QJS_GetArrayBuffer"
    "LINKER:--export=QJS_GetOwnPropertyAtoms"
    "LINKER:--export=QJS_FreeAtoms"
    "LINKER:--export=js_free"
    "LINKER:--export=QJS_GetPropertyLen"
    "LINKER:--export=QJS_SetPropertyLen"
    "LINKER:--export=QJS_HasPropertyLen"
    "LINKER:--export=QJS_DeletePropertyLen"
    "LINKER:--export=QJS_GetPropertyValue"
    "LINKER:--export=QJS_SetPropertyValue"
    "LINKER:--export=JS_GetPropertyUint32"
    "LINKER:--export=QJS_SetPropertyUint32"
    "LINKER:--export=QJS_ToInt64"
    "LINKER:--export=QJS_ToInt32"
    "LINKER:--export=QJS_ToUint32"
    "LINKER:--export=QJS_ToFloat64"
    "LINKER:--export=QJS_ToEpochTime"
    "LINKER:--export=JS_NewStringLen"
    "LINKER:--export=JS_NewObject"
    "LINKER:--export=JS_NewArray"
    "LINKER:--export=JS_NewDate"
    "LINKER:--export=JS_NewError"
    "LINKER:--export=JS_NewBigInt64"
    "LINKER:--export=JS_NewBigUint64"
    "LINKER:--export=JS_NewArrayBufferCopy"
    "LINKER:--export=QJS_ParseJSON"
    "LINKER:--export=QJS_NewBigIntDecimal"
    "LINKER:--export=JS_Call"
    "LINKER:--export=JS_CallConstructor"
    "LINKER:--export=QJS_NewGlobal"
    "LINKER:--export=QJS_NewGoFunction"
    "LINKER:--export=QJS_NewProxyValue"
    "LINKER:--export=QJS_GetProxyID"
    "LINKER:--export=QJS_GetKind"
)

target_compile_options(qjswasm PRIVATE "-fvisibility=default")

target_compile_definitions(qjswasm PRIVATE ${qjs_defines})

target_link_libraries(qjswasm qjs)

if(NOT WIN32)
    set_target_properties(qjswasm PROPERTIES ENABLE_EXPORTS TRUE)
endif()

if(QJS_BUILD_CLI_WITH_MIMALLOC OR QJS_BUILD_CLI_WITH_STATIC_MIMALLOC)
    find_package(mimalloc REQUIRED)
    if(QJS_BUILD_CLI_WITH_STATIC_MIMALLOC)
        target_link_libraries(qjswasm mimalloc-static)
    else()
        target_link_libraries(qjswasm mimalloc)
    endif()
endif()

set(CMAKE_BUILD_TYPE "Release" CACHE STRING "" FORCE)
set(CMAKE_INTERPROCEDURAL_OPTIMIZATION ON)  # enables -flto for clang/wasm-ld

add_compile_options(-O3 -DNDEBUG)
add_link_options(-O3)

add_link_options("LINKER:--stack-first")
add_link_options("LINKER:--initial-memory=1310720") # 20 pages
