package healthz

import "errors"

var errNoYetChecked = errors.New("not yet checked")

type healthResult struct {
	startUp []error
	live    []error
	ready   []error
}

func newHealthResult() *healthResult {
	return &healthResult{
		startUp: []error{errNoYetChecked},
		live:    []error{errNoYetChecked},
		ready:   []error{errNoYetChecked},
	}
}

func (hr *healthResult) add(res serviceCheckResult) {
	if res.target.Groups&GroupStartup != 0 {
		hr.startUp = append(hr.startUp, res.err)
	}

	if res.target.Groups&GroupLive != 0 {
		hr.live = append(hr.live, res.err)
	}

	if res.target.Groups&GroupReady != 0 {
		hr.ready = append(hr.ready, res.err)
	}
}

func (hr *healthResult) health(group ProbeGroup, needAllHealthy bool) error {
	var list []error

	switch {
	case group&GroupLive != 0:
		list = hr.live
	case group&GroupReady != 0:
		list = hr.ready
	case group&GroupStartup != 0:
		list = hr.startUp
	}

	if needAllHealthy {
		return accureError(list)
	}

	return accureNoError(list)
}

func accureError(list []error) error {
	return errors.Join(list...)
}

func accureNoError(list []error) error {
	for _, e := range list {
		if e == nil {
			return nil
		}
	}

	return errors.Join(list...)
}
