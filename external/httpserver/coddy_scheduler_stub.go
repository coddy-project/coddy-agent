//go:build http && !scheduler

package httpserver

// registerSchedulerRoutes is a no-op when the binary is built without scheduler support.
func (s *Server) registerSchedulerRoutes() {}
