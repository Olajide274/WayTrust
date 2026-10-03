package observability

import (
	"encoding/json"
	"log"
	"os"
	"time"
)

type Fields map[string]any

func logLine(level, msg string, fields Fields) {
	entry := map[string]any{
		"ts":    time.Now().UTC().Format(time.RFC3339),
		"level": level,
		"msg":   msg,
	}
	for k, v := range fields {
		entry[k] = v
	}
	b, err := json.Marshal(entry)
	if err != nil {
		log.Printf(`{"level":"error","msg":"log_marshal_failed","error":%q}`, err.Error())
		return
	}
	_, _ = os.Stdout.Write(append(b, '\n'))
}

func Info(msg string, fields Fields) {
	logLine("info", msg, fields)
}

func Warn(msg string, fields Fields) {
	logLine("warn", msg, fields)
}

func Error(msg string, fields Fields) {
	logLine("error", msg, fields)
}