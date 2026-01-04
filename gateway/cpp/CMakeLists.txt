cmake_minimum_required(VERSION 3.16)
project(imessage_gateway_cpp LANGUAGES CXX)

set(CMAKE_CXX_STANDARD 17)
set(CMAKE_CXX_STANDARD_REQUIRED ON)
set(CMAKE_EXPORT_COMPILE_COMMANDS ON)

add_executable(imessage_gateway main.cpp)
target_include_directories(imessage_gateway PRIVATE ${CMAKE_CURRENT_SOURCE_DIR}/include)

find_package(SQLite3)
if(SQLite3_FOUND)
  target_link_libraries(imessage_gateway PRIVATE SQLite::SQLite3)
else()
  target_link_libraries(imessage_gateway PRIVATE sqlite3)
endif()
