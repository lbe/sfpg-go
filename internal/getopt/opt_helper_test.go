package getopt

func getOsExit() func(int) {
	return osExit
}

func setOsExit(fn func(int)) {
	osExit = fn
}

func getUsageExit() func(string) {
	return usageExit
}

func setUsageExit(fn func(string)) {
	usageExit = fn
}

func getOsGOOS() func() string {
	return osGOOS
}

func setOsGOOS(fn func() string) {
	osGOOS = fn
}

func getOsExecutable() func() (string, error) {
	return osExecutable
}

func setOsExecutable(fn func() (string, error)) {
	osExecutable = fn
}
