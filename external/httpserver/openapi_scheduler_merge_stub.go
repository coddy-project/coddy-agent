//go:build http && !scheduler

package httpserver

func mergeOpenAPISchedulerDoc(_ *map[string]interface{}) {}
