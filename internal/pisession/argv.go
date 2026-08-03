package pisession

func buildArgv(piBin, sessionID, sessionDir, model, thinking string, extraArgs []string) []string {
	argv := make([]string, 0, 7+len(extraArgs)+4)
	argv = append(argv,
		piBin,
		"--mode", "rpc",
		"--session-id", sessionID,
		"--session-dir", sessionDir,
	)
	if model != "" {
		argv = append(argv, "--model", model)
	}
	if thinking != "" {
		argv = append(argv, "--thinking", thinking)
	}
	return append(argv, extraArgs...)
}
