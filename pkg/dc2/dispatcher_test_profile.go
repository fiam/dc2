package dc2

import (
	"strings"

	"github.com/fiam/dc2/pkg/dc2/testprofile"
)

func (d *Dispatcher) currentTestProfileYAML() (string, bool) {
	d.testProfileMu.RLock()
	defer d.testProfileMu.RUnlock()

	if d.testProfile == nil {
		return "", false
	}
	return d.testProfileYAML, true
}

func (d *Dispatcher) updateTestProfileFromYAML(raw string) error {
	profile, err := testprofile.LoadYAML(raw)
	if err != nil {
		return err
	}
	d.setTestProfile(profile, raw)
	return nil
}

func (d *Dispatcher) clearTestProfile() {
	d.testProfileMu.Lock()
	d.testProfile = nil
	d.testProfileYAML = ""
	d.testProfileMu.Unlock()
	d.notifyTestProfileUpdated()
}

func (d *Dispatcher) setTestProfile(profile *testprofile.Profile, rawYAML string) {
	d.testProfileMu.Lock()
	d.testProfile = profile
	d.testProfileYAML = strings.TrimSpace(rawYAML)
	d.testProfileMu.Unlock()
	d.notifyTestProfileUpdated()
}

func (d *Dispatcher) activeTestProfile() *testprofile.Profile {
	d.testProfileMu.RLock()
	defer d.testProfileMu.RUnlock()
	return d.testProfile
}

func (d *Dispatcher) notifyTestProfileUpdated() {
	select {
	case d.testProfileUpdateCh <- struct{}{}:
	default:
	}
}
