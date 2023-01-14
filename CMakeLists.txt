cmake_minimum_required(VERSION 3.10)
project(CemuMegaDownloader)

list(APPEND CMAKE_MODULE_PATH ${CMAKE_SOURCE_DIR}/cmake)

find_package(MbedTLS)

link_directories(
    ${GTKMM_LIBRARY_DIRS}
    /usr/local/lib
    /opt/homebrew/lib
)

include_directories(
    ${CMAKE_CURRENT_SOURCE_DIR}
    ${GTKMM_INCLUDE_DIRS}
    include
    /usr/local/include
    /opt/homebrew/include
)

file(GLOB SOURCES
    src/*.cpp
    src/*.c
    src/cdecrypt/*.c
)

set(CMAKE_C_FLAGS "${CMAKE_C_FLAGS} -D_GNU_SOURCE -UNDEBUG -DAES_ROM_TABLES -fvisibility=hidden -Ofast -fno-strict-aliasing -pipe")
set(CMAKE_CXX_FLAGS "${CMAKE_CXX_FLAGS} -D_GNU_SOURCE -UNDEBUG -DAES_ROM_TABLES -fvisibility=hidden -Ofast -fno-strict-aliasing -pipe -std=c++20")

add_executable(${CMAKE_PROJECT_NAME}
                ${SOURCES}
                ${RESOURCE_FILE})

target_link_libraries(${CMAKE_PROJECT_NAME}
    ${GTKMM_LIBRARIES}
    stdc++
    mbedtls
    mbedcrypto
    mbedx509
    curl
)