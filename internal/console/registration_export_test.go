package console

// Registered is the catalogue, flattened for the render half of the
// registration test — which cannot be in-package, because it boots a Stack
// and dozeaws imports console.
//
// Derived from `catalog` and `surfaces` rather than restating them, for the
// reason the whole test exists: a second hand-written list is exactly the
// defect being fixed.

// Registration is one entry the render test walks.
type Registration struct {
	Key string
	// CreatePath is prefix-relative and empty when the service has no create
	// form.
	CreatePath string
	// Path is the service's own page, prefix-relative.
	Path string
}

// RegisteredServices is every service in the catalogue.
func RegisteredServices() []Registration {
	out := make([]Registration, 0, len(catalog))
	for _, e := range catalog {
		out = append(out, Registration{Key: e.Key, CreatePath: e.CreatePath, Path: "/" + e.Key})
	}
	return out
}

// RegisteredSurfaces is the non-service pages — the wire and Connect. They
// have no icon, no colour and no create form, so they only get the render
// check.
func RegisteredSurfaces() []Registration {
	out := make([]Registration, 0, len(surfaces))
	for _, e := range surfaces {
		path := "/" + e.Key
		if e.Key == "traffic" {
			path = "/" // the wire is the root
		}
		out = append(out, Registration{Key: e.Key, Path: path})
	}
	return out
}
