cmake_minimum_required(VERSION 3.16)
project(sonic_bridge_console CXX)

set(CMAKE_CXX_STANDARD 17)
set(CMAKE_CXX_STANDARD_REQUIRED ON)
set(CMAKE_CXX_EXTENSIONS OFF)

find_package(PkgConfig REQUIRED)
pkg_check_modules(PORTAUDIO REQUIRED IMPORTED_TARGET portaudio-2.0)
find_package(Threads REQUIRED)

add_executable(console src/main.cpp)
target_link_libraries(console PRIVATE PkgConfig::PORTAUDIO Threads::Threads)
target_compile_options(console PRIVATE -Wall -Wextra -Wpedantic)
