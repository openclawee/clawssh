// Package adapter hosts ToolProvider implementations ("plugins") that execute dsl.Task values.
//
// The gateway kernel (see internal/server) depends only on the ToolProvider interface, not on
// concrete transports. New backends (Ansible, Kubernetes, cloud APIs) should live here as
// separate types implementing ToolProvider, keeping execution strategies swappable and testable.
package adapter
