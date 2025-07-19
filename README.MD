# ULOG - Advanced Go Logging Library

[![Go Version](https://img.shields.io/badge/go-1.24+-blue.svg)](https://golang.org)
[![License](https://img.shields.io/badge/license-MIT-green.svg)](LICENSE)

ULOG is a powerful and flexible Go logging library built on top of the standard `log/slog` package. It provides structured logging with Symfony Monolog-compatible format, multiple output formats, file rotation, and advanced handler capabilities.

## Features

- 🚀 **Built on Go's standard `log/slog`** - Full compatibility with slog ecosystem
- 📝 **Multiple Output Formats** - JSON and human-readable text formats
- 🔄 **File Rotation** - Automatic log rotation with size, backup count, and age limits
- 🎯 **Symfony Monolog Compatible** - Follows PHP Symfony Monolog structure
- 🔗 **Context Support** - Pass structured data through Go contexts
- 📡 **Multi-Handler** - Send logs to multiple destinations simultaneously
- ⚡ **Thread-Safe** - Concurrent logging support with mutex protection
- 🛠️ **Highly Configurable** - Flexible configuration options

## Installation

```bash
go get github.com/vitiok78/ulog
```

## Quick Start

### Basic Usage

```go
package main

import (
    "log/slog"
    "os"
    "github.com/vitiok78/ulog"
)

func main() {
    // Create a text logger
    handler := ulog.NewTextLogger(os.Stdout, "app")
    logger := slog.New(handler)
    
    logger.Info("Application started", "version", "1.0.0")
    logger.Error("Something went wrong", "error", "connection failed")
}
```

### JSON Logging

```go
// Create a JSON logger
handler := ulog.NewJSONLogger(os.Stdout, "api")
logger := slog.New(handler)

logger.Info("User login", "user_id", 123, "ip", "192.168.1.1")
// Output: {"message":"User login","context":{"user_id":123,"ip":"192.168.1.1"},"level":200,"level_name":"INFO","channel":"api","datetime":"2024-01-15T10:30:45.123456789Z","extra":{}}
```

### File Logging with Rotation

```go
// Create a file logger with rotation
handler, err := ulog.NewFileLogger(
    "/var/log/app.log",  // file path
    "json",              // format: "json" or "text"
    "app",               // channel name
    10*1024*1024,        // max size: 10MB
    5,                   // max backups: keep 5 old files
    30,                  // max age: 30 days
)
if err != nil {
    panic(err)
}

logger := slog.New(handler)
logger.Info("File logging enabled")
```

## Advanced Usage

### Custom Configuration

```go
config := ulog.Config{
    Formatter:  ulog.NewJSONFormatter(),
    Writer:     os.Stderr,
    Channel:    "custom",
    CtxKeyName: "context",
    AddSource:  true, // Add source file information
}

handler := ulog.New(config)
logger := slog.New(handler)
```

### Multi-Handler Setup

```go
// Send logs to multiple destinations
fileHandler, _ := ulog.NewFileLogger("/var/log/app.log", "json", "app", 10*1024*1024, 5, 30)
consoleHandler := ulog.NewTextLogger(os.Stdout, "app")

multiHandler := ulog.NewMultiHandler(fileHandler, consoleHandler)
logger := slog.New(multiHandler)

logger.Info("This will be logged to both file and console")
```

### Context-Based Logging

```go
import (
    "context"
    "log/slog"
    "github.com/vitiok78/ulog"
)

// Add attributes to context
ctx := ulog.WithAttrsContext(context.Background(),
    slog.String("request_id", "req-123"),
    slog.String("user_id", "user-456"),
)

// These attributes will be included in all logs using this context
handler := ulog.NewJSONLogger(os.Stdout, "api")
logger := slog.New(handler)

logger.InfoContext(ctx, "Processing request")
// The request_id and user_id will be automatically included
```

### Custom Formatters

```go
// JSON formatter with custom time format
jsonFormatter := ulog.NewJSONFormatter()
jsonFormatter.TimeFormat = "2006-01-02 15:04:05"

// Text formatter with custom time format
textFormatter := ulog.NewTextFormatter()
textFormatter.TimeFormat = "2006-01-02 15:04:05"

config := ulog.Config{
    Formatter: jsonFormatter,
    Writer:    os.Stdout,
    Channel:   "app",
}
```

## Log Levels

ULOG maps Go's slog levels to Symfony Monolog levels:

| slog Level | Monolog Level | Monolog Name |
|------------|---------------|--------------|
| DEBUG      | 100           | DEBUG        |
| INFO       | 200           | INFO         |
| WARN       | 300           | WARNING      |
| ERROR      | 400           | ERROR        |
| Higher     | 500           | CRITICAL     |

## Output Formats

### Text Format
```
[2024-01-15T10:30:45.123456789Z] app.INFO: User logged in {"user_id":123,"action":"login"} []
```

### JSON Format
```json
{
  "message": "User logged in",
  "context": {"user_id": 123, "action": "login"},
  "level": 200,
  "level_name": "INFO",
  "channel": "app",
  "datetime": "2024-01-15T10:30:45.123456789Z",
  "extra": {}
}
```

## File Rotation

The file writer supports automatic rotation based on:

- **Size**: Rotate when file reaches maximum size
- **Age**: Rotate files older than specified days
- **Backup Count**: Keep only specified number of backup files

### Size Parsing

The `ParseByteSize` function supports various size formats:
```go
size, err := ulog.ParseByteSize("10MB")  // 10 megabytes
size, err := ulog.ParseByteSize("1GB")   // 1 gigabyte
size, err := ulog.ParseByteSize("500KB") // 500 kilobytes
```

## Configuration Options

### Config Struct

```go
type Config struct {
    Formatter  Formatter    // Log formatter (JSON or Text)
    Writer     io.Writer    // Output destination
    Channel    string       // Channel name for logs
    CtxKeyName string       // Context key name for attributes
    AddSource  bool         // Include source file information
}
```

### Default Configuration

```go
config := ulog.DefaultConfig()
// Returns:
// - TextFormatter
// - os.Stderr output
// - "app" channel
// - "ctx" context key name
// - AddSource: false
```

## Thread Safety

All handlers are thread-safe and can be used concurrently across multiple goroutines. Internal mutex protection ensures data integrity during concurrent logging operations.

## Integration with Standard Library

ULOG handlers implement the standard `slog.Handler` interface, making them fully compatible with Go's logging ecosystem:

```go
// Use with standard slog
logger := slog.New(ulog.NewJSONLogger(os.Stdout, "app"))

// Use with slog default logger
slog.SetDefault(logger)
slog.Info("This uses ulog handler")
```

## Error Handling

```go
// File logger creation with error handling
handler, err := ulog.NewFileLogger("/var/log/app.log", "json", "app", 10*1024*1024, 5, 30)
if err != nil {
    // Handle error (e.g., permission issues, invalid path)
    log.Fatalf("Failed to create file logger: %v", err)
}

// Always close file writers when done
if closer, ok := handler.(*ulog.Handler); ok {
    defer closer.Close() // If the writer implements io.Closer
}
```

## Best Practices

1. **Use structured logging**: Pass key-value pairs instead of formatting strings
2. **Set appropriate log levels**: Use DEBUG for development, INFO+ for production
3. **Configure rotation**: Set reasonable file size and backup limits
4. **Use contexts**: Pass request-scoped data through contexts
5. **Handle errors**: Always check errors when creating file loggers
6. **Close resources**: Ensure file writers are properly closed

## License

This project is licensed under the MIT License - see the [LICENSE](LICENSE) file for details.

## Contributing

Contributions are welcome! Please feel free to submit a Pull Request.