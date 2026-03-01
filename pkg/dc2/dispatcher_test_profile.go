package dc2

import (
	"strings"

	"github.com/fiam/dc2/pkg/dc2/testprofile"
)

func (d *Dispatcher) currentTestProfileYAML() (string, bool) {
	d.dispatchMu.Lock()
	defer d.dispatchMu.Unlock()

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

	d.dispatchMu.Lock()
	defer d.dispatchMu.Unlock()

	d.testProfile = profile
	d.testProfileYAML = strings.TrimSpace(raw)
	return nil
}

func (d *Dispatcher) clearTestProfile() {
	d.dispatchMu.Lock()
	defer d.dispatchMu.Unlock()

	d.testProfile = nil
	d.testProfileYAML = ""
}
