package runtime

// Registry holds multiple runtime drivers keyed by backend name.
type Registry struct {
	drivers        map[string]Driver
	defaultBackend string
}

// NewRegistry constructs an empty registry.
func NewRegistry() *Registry {
	return &Registry{drivers: make(map[string]Driver)}
}

// Register adds a driver to the registry. The first registered driver becomes
// the default backend.
func (r *Registry) Register(driver Driver) {
	backend := driver.Backend()
	r.drivers[backend] = driver
	if r.defaultBackend == "" {
		r.defaultBackend = backend
	}
}

// Driver returns the driver for the given backend, or the default driver if
// backend is empty. The second return value indicates whether a driver was
// found.
func (r *Registry) Driver(backend string) (Driver, bool) {
	if backend == "" {
		backend = r.defaultBackend
	}
	d, ok := r.drivers[backend]
	return d, ok
}

// DefaultBackend returns the default backend name (the first registered driver,
// or the one explicitly set via SetDefaultBackend).
func (r *Registry) DefaultBackend() string {
	return r.defaultBackend
}

// SetDefaultBackend explicitly sets the default backend. Returns false if the
// backend is not registered.
func (r *Registry) SetDefaultBackend(backend string) bool {
	if _, ok := r.drivers[backend]; !ok {
		return false
	}
	r.defaultBackend = backend
	return true
}

// Capabilities returns a list of available runtime capabilities in the form
// "runtime.<backend>".
func (r *Registry) Capabilities() []string {
	caps := make([]string, 0, len(r.drivers))
	for backend := range r.drivers {
		caps = append(caps, "runtime."+backend)
	}
	return caps
}

// HasBackend reports whether the registry contains a driver for the given backend.
func (r *Registry) HasBackend(backend string) bool {
	_, ok := r.drivers[backend]
	return ok
}

// Backends returns all registered backend names.
func (r *Registry) Backends() []string {
	backends := make([]string, 0, len(r.drivers))
	for b := range r.drivers {
		backends = append(backends, b)
	}
	return backends
}
