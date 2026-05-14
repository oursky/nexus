//go:build !linux

package daemon

import "io"

func implodeHostDatastore(_ io.Writer) {}

func implodeSystemUnits(_ io.Writer) {}
