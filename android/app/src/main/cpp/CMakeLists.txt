cmake_minimum_required(VERSION 3.22.1)
project(omnirouter_jni C)

add_library(omnirouter SHARED IMPORTED GLOBAL)
set_target_properties(omnirouter PROPERTIES
    IMPORTED_LOCATION "${CMAKE_CURRENT_SOURCE_DIR}/../jniLibs/${ANDROID_ABI}/libomnirouter.so"
)

add_library(omnirouter_jni SHARED jni_bridge.c)
target_link_libraries(omnirouter_jni omnirouter log)
