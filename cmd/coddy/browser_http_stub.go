//go:build !http

package main

import (
	"net/http"

	"github.com/EvilFreelancer/coddy-agent/internal/serve"
)

func browserChatHandler(_ *serve.Runtime) (http.Handler, func()) { return nil, func() {} }
