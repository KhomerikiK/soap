package model

import "testing"

const chromeHelperPath = "/Applications/Google Chrome.app/Contents/Frameworks/Google Chrome Framework.framework/Versions/142/Helpers/Google Chrome Helper.app/Contents/MacOS/Google Chrome Helper"

func TestAppPathAndName(t *testing.T) {
	if got := AppPath(chromeHelperPath); got != "/Applications/Google Chrome.app" {
		t.Errorf("AppPath = %q", got)
	}
	if got := AppName("/Applications/Google Chrome.app"); got != "Google Chrome" {
		t.Errorf("AppName = %q", got)
	}
	if got := AppPath("/usr/sbin/cupsd"); got != "" {
		t.Errorf("AppPath(cupsd) = %q, want empty", got)
	}
}

func TestIsSystem(t *testing.T) {
	if !IsSystem(proc(400, 88, "WindowServer", "/System/Library/PrivateFrameworks/SkyLight.framework/Resources/WindowServer")) {
		t.Error("WindowServer should be a system process")
	}
	if !IsSystem(proc(0, 0, "kernel_task", "")) {
		t.Error("root process without a path should be a system process")
	}
	if IsSystem(proc(48121, 501, "node", "/usr/local/bin/node")) {
		t.Error("node isn't a system process")
	}
}

func chromeProcs() (main, helper1, helper2 Process) {
	main = proc(100, 501, "Google Chrome", "/Applications/Google Chrome.app/Contents/MacOS/Google Chrome")
	main.Memory = 500
	helper1 = proc(101, 501, "Google Chrome Helper", chromeHelperPath)
	helper1.Memory, helper1.CPU = 300, 2.5
	helper2 = helper1
	helper2.PID = 102
	return main, helper1, helper2
}

func TestGroupByApp(t *testing.T) {
	main, helper1, helper2 := chromeProcs()
	ws := proc(400, 88, "WindowServer", "/System/Library/PrivateFrameworks/SkyLight.framework/Resources/WindowServer")
	ws.Memory = 700
	node := proc(48121, 501, "node", "/usr/local/bin/node")
	node.Memory = 100
	// A helper comes first, to check that Main still ends up on the app's main executable.
	apps := GroupByApp([]Process{helper1, main, helper2, ws, node}, false)
	if len(apps) != 3 {
		t.Fatalf("got %d rows, want 3: %+v", len(apps), apps)
	}
	c := apps[0]
	if c.Name != "Google Chrome" || c.AppPath != "/Applications/Google Chrome.app" || c.Memory != 1100 || c.CPU != 5 || len(c.PIDs) != 3 || c.Main.PID != 100 {
		t.Errorf("chrome = %+v", c)
	}
	if apps[1].Name != "WindowServer" || !apps[1].System {
		t.Errorf("second row = %+v", apps[1])
	}
	if apps[2].Name != "node" || apps[2].System || apps[2].AppPath != "" {
		t.Errorf("third row = %+v", apps[2])
	}
}

func TestGroupByAppRaw(t *testing.T) {
	main, helper1, _ := chromeProcs()
	apps := GroupByApp([]Process{helper1, main}, true)
	if len(apps) != 2 {
		t.Fatalf("got %d rows, want 2", len(apps))
	}
	if apps[0].Name != "Google Chrome" || apps[1].Name != "Google Chrome Helper" {
		t.Errorf("names = %q, %q", apps[0].Name, apps[1].Name)
	}
	if apps[0].AppPath != "" || apps[1].Main.PID != 101 {
		t.Errorf("raw rows act on single processes: %+v", apps)
	}
}

func TestGroupByAppMainIsLowestPID(t *testing.T) {
	path := "/Applications/Google Chrome.app/Contents/MacOS/Google Chrome"
	first := proc(300, 501, "Google Chrome", path)
	second := proc(100, 501, "Google Chrome", path)
	apps := GroupByApp([]Process{first, second}, false)
	if len(apps) != 1 || apps[0].Main.PID != 100 {
		t.Errorf("Main = %+v, want the lowest PID", apps)
	}
	apps = GroupByApp([]Process{second, first}, false)
	if apps[0].Main.PID != 100 {
		t.Errorf("Main = %+v, want the lowest PID in any order", apps)
	}
}
