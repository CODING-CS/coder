// go:build windows

package tz

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"time"

	"golang.org/x/xerrors"
)

const cmdTimezone = "[Windows.Globalization.Calendar,Windows.Globalization,ContentType=WindowsRuntime]::New().GetTimeZone()"

// TimezoneIANA attempts to determine the local timezone in IANA format.
// If the TZ environment variable is set, this is used.
// Otherwise, /etc/localtime is used to determine the timezone.
// Reference: https://stackoverflow.com/a/63805394
// On Windows platforms, instead of reading /etc/localtime, powershell
// is used instead to get the current time location in IANA format.
// Reference: https://superuser.com/a/1584968
func TimezoneIANA() (*time.Location, error) {
	if tzEnv, found := os.LookupEnv("TZ"); found {
		if tzEnv == "" {
			return time.UTC, nil
		}
		loc, err := time.LoadLocation(tzEnv)
		if err != nil {
			return nil, xerrors.Errorf("load location from TZ env: %w", err)
		}
		return loc, nil
	}

	// https://superuser.com/a/1584968
	cmd := exec.Command("powershell.exe", "-nologo", "-noprofile")
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, xerrors.Errorf("run powershell: %w", err)
	}

	done := make(chan struct{})
	go func() {
		defer stdin.Close()
		defer close(done)
		_, _ = fmt.Fprintln(stdin, cmdTimezone)
	}()

	<-done

	out, err := cmd.CombinedOutput()
	if err != nil {
		return nil, xerrors.Errorf("execute powershell command %q: %w", cmdTimezone, err)
	}

	locStr := string(bytes.TrimSpace(out))
	loc, err := time.LoadLocation(locStr)
	if err != nil {
		return nil, xerrors.Errorf("invalid location %q from powershell: %w", locStr, err)
	}

	return loc, nil
}
