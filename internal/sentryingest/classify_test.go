package sentryingest

import (
	"testing"

	"supermetry/internal/appingest"
)

func TestClassifyTerminationsAreCrashExitReasons(t *testing.T) {
	no, yes := false, true
	exc := func(typ, module, value, mech string, handled *bool) appingest.Exception {
		e := appingest.Exception{Type: typ, Module: module, Value: value}
		if mech != "" || handled != nil {
			e.Mechanism = &appingest.Mechanism{Type: mech, Handled: handled}
		}
		return e
	}
	cases := []struct {
		name       string
		level      string
		excs       []appingest.Exception
		kind, exit string
	}{
		{"watchdog by type", "fatal", []appingest.Exception{exc("WatchdogTermination", "", "", "", nil)}, "crash", "watchdog_termination"},
		{"watchdog by mechanism", "", []appingest.Exception{exc("x", "", "", "watchdog_termination", nil)}, "crash", "watchdog_termination"},
		{"java oom, unhandled", "fatal", []appingest.Exception{exc("OutOfMemoryError", "java.lang", "Failed to allocate", "UncaughtExceptionHandler", &no)}, "crash", "oom"},
		{"java oom qualified type", "fatal", []appingest.Exception{exc("java.lang.OutOfMemoryError", "", "", "", nil)}, "crash", "oom"},
		{"java oom wrapped in the chain", "fatal", []appingest.Exception{exc("OutOfMemoryError", "java.lang", "", "", nil), exc("RuntimeException", "java.lang", "", "", nil)}, "crash", "oom"},
		{"java oom caught and handled stays an error", "error", []appingest.Exception{exc("OutOfMemoryError", "java.lang", "", "generic", &yes)}, "error", ""},
		{"exc_resource memory", "fatal", []appingest.Exception{exc("EXC_RESOURCE", "", "RESOURCE_TYPE_MEMORY (limit=1400 MB)", "mach", &no)}, "crash", "oom"},
		{"exc_resource cpu is a plain crash", "fatal", []appingest.Exception{exc("EXC_RESOURCE", "", "RESOURCE_TYPE_CPU", "mach", &no)}, "crash", ""},
		{"memory limiter mechanism", "fatal", []appingest.Exception{exc("x", "", "", "MemoryLimiter", &no)}, "crash", "oom"},
		{"anr stays a hang", "", []appingest.Exception{exc("ApplicationNotResponding", "", "", "anr", &no)}, "hang", "anr"},
		{"app hang stays a hang", "", []appingest.Exception{exc("App Hanging", "", "", "AppHang", nil)}, "hang", "app_hang"},
		{"plain fatal", "fatal", []appingest.Exception{exc("NullPointerException", "java.lang", "", "", &no)}, "crash", ""},
	}
	for _, c := range cases {
		kind, exit := classify(c.level, c.excs, "android", false)
		if kind != c.kind || exit != c.exit {
			t.Errorf("%s: got %s/%q, want %s/%q", c.name, kind, exit, c.kind, c.exit)
		}
	}
}
