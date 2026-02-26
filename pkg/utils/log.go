package utils

import (
	"github.com/winking324/socket.io/v3/pkg/log"
)

func Log() *log.Log {
	return log.Default()
}
