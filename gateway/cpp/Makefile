PY_CFLAGS := $(shell python3-config --includes)
PY_LDFLAGS := $(shell python3-config --embed --ldflags 2>/dev/null || python3-config --ldflags)

TARGET := imessage_gateway
SOURCES := imessage_gateway.cpp

$(TARGET): $(SOURCES)
	@echo "Building $(TARGET) with $(PY_CFLAGS) $(PY_LDFLAGS)"
	@g++ -std=c++17 -O2 $(SOURCES) -o $(TARGET) $(PY_CFLAGS) $(PY_LDFLAGS)

.PHONY: clean
clean:
	@rm -f $(TARGET)
