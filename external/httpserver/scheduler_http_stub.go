//go:build http && !scheduler

package httpserver

// registerSchedulerRoutes is a no-op when the binary is built without scheduler support.
func (s *Server) registerSchedulerRoutes() {}

// mergeOpenAPISchedulerDoc is a no-op without the scheduler tag (no extra paths in served OpenAPI).
func mergeOpenAPISchedulerDoc(_ *map[string]interface{}) {}
