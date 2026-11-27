package serve

import "testing"

func TestWindowsServiceHome(t *testing.T) {
	t.Parallel()
	image := `"C:\Program Files\Coddy\coddy.exe" __windows-service --home "D:\Coddy Home" --config "D:\Coddy Home\config.yaml"`
	if got, want := windowsServiceHome(image), `D:\Coddy Home`; got != want {
		t.Fatalf("home = %q, want %q", got, want)
	}
	state := WindowsServiceState{Installed: true, Home: `d:\coddy home`}
	if !state.ServesHome(`D:\CODDY HOME`) || state.ServesHome(`D:\Other`) {
		t.Fatal("service home identity did not compare the complete path")
	}
	if got := windowsServiceHome(`"C:\coddy.exe" __windows-service --home "D:\broken`); got != "" {
		t.Fatalf("malformed command line home = %q", got)
	}
	if got := windowsServiceHome(`"C:\coddy.exe" --home D:\other`); got != "" {
		t.Fatalf("non-service command home = %q", got)
	}
}

func TestSameServiceExecutable(t *testing.T) {
	t.Parallel()
	exe := `C:\Program Files\Coddy\coddy.exe`
	for _, tc := range []struct {
		name      string
		imagePath string
		want      bool
	}{
		{name: "quoted with arguments", imagePath: `"C:\Program Files\Coddy\coddy.exe" __windows-service --home "C:\Users\me\.coddy"`, want: true},
		{name: "unquoted with arguments", imagePath: `C:\Program Files\Coddy\coddy.exe __windows-service`, want: true},
		{name: "case insensitive", imagePath: `"c:\program files\coddy\CODDY.EXE"`, want: true},
		{name: "escaped closing quote", imagePath: `"C:\Program Files\Coddy\\"`, want: false},
		{name: "different executable", imagePath: `"C:\Program Files\Other\coddy.exe"`, want: false},
		{name: "path is not a substring match", imagePath: `C:\Program Files\Coddy\coddy.exe.backup __windows-service`, want: false},
		{name: "argument is not the executable", imagePath: `"C:\Windows\System32\cmd.exe" /c C:\Program Files\Coddy\coddy.exe`, want: false},
		{name: "empty image", imagePath: "", want: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := sameServiceExecutable(tc.imagePath, exe); got != tc.want {
				t.Fatalf("sameServiceExecutable(%q, %q) = %v, want %v", tc.imagePath, exe, got, tc.want)
			}
		})
	}
}
