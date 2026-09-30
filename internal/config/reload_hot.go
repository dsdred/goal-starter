package config

// HotReloadDiff classifies the difference between a freshly read config file
// and the currently effective live configuration (ADR 009 D1/D3).
// Applied: hot fields a reload applies immediately (field names).
// RestartRequired: restart-class fields whose file value differs from the
// live value (field names). Both slices are non-nil and deterministically
// ordered (Config struct field order).
type HotReloadDiff struct {
	Applied         []string
	RestartRequired []string
}

// DiffHot computes the hot/restart classification of file vs live.
// Credential material (adminPasswordHash) is intentionally not classified:
// a reload never applies it (ADR 009 D1). The seed sections (runtimes,
// models, profiles) are never re-applied and are not classified either.
func DiffHot(file, live Config) HotReloadDiff {
	applied := []string{}
	restart := []string{}
	if file.LogLevel != live.LogLevel {
		applied = append(applied, "logLevel")
	}
	if file.ListenAddress != live.ListenAddress {
		restart = append(restart, "listenAddress")
	}
	if file.WebPort != live.WebPort {
		restart = append(restart, "webPort")
	}
	if file.DataDir != live.DataDir {
		restart = append(restart, "dataDir")
	}
	if file.AuthEnabled != live.AuthEnabled {
		restart = append(restart, "authEnabled")
	}
	if file.AdminUser != live.AdminUser {
		restart = append(restart, "adminUser")
	}
	if tlsDiffers(file.TLS, live.TLS) {
		restart = append(restart, "tls")
	}
	return HotReloadDiff{Applied: applied, RestartRequired: restart}
}

// tlsDiffers compares the optional tls blocks field by field. tls is
// restart-required (ADR 019 §D4, §D23), so any difference in the block —
// including appearing or disappearing — must be reported rather than silently
// treated as "no change". The fields are compared individually because Port is
// a pointer: comparing the structs would compare port addresses, and two blocks
// parsed from the same text would then look different on every reload.
func tlsDiffers(a, b *TLSConfig) bool {
	if a == nil || b == nil {
		return a != b
	}
	if a.Enabled != b.Enabled || a.CertFile != b.CertFile || a.KeyFile != b.KeyFile {
		return true
	}
	return portDiffers(a.Port, b.Port)
}

func portDiffers(a, b *int) bool {
	if a == nil || b == nil {
		return a != b
	}
	return *a != *b
}
