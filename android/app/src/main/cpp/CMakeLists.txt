cmake_minimum_required(VERSION 3.22.1)
project(omnirouter_jni C)

# The Go c-shared library is packaged beside this JNI library in the APK.
# Do not let CMake encode the build-machine IMPORTED_LOCATION as DT_NEEDED.
# The Go library is built with SONAME=libomnirouter.so, which Android's linker
# resolves from the app's native library directory after loadCoreLibrary().
add_library(omnirouter SHARED IMPORTED GLOBAL)
set_target_properties(omnirouter PROPERTIES
    IMPORTED_LOCATION "${CMAKE_CURRENT_SOURCE_DIR}/../jniLibs/${ANDROID_ABI}/libomnirouter.so"
    IMPORTED_NO_SONAME TRUE
)

add_library(omnirouter_jni SHARED jni_bridge.c)
target_link_libraries(omnirouter_jni omnirouter log)
