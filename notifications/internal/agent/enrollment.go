package agent

import (
	"context"
	"errors"
	"os"
	"time"
)

type enrollmentResult int

const (
	enrollmentWaiting enrollmentResult = iota
	enrollmentSucceeded
	enrollmentRetry
)

func (d *Daemon) setDefaults() {
	if d.now == nil {
		d.now = time.Now
	}
	if d.Client == nil {
		d.Client = DefaultHTTPClient()
	}
	if d.wake == nil {
		d.wake = make(chan struct{}, 1)
	}
	if d.enrollmentPoll <= 0 {
		d.enrollmentPoll = 5 * time.Second
	}
	if d.enrollmentMin <= 0 {
		d.enrollmentMin = time.Second
	}
	if d.enrollmentMax <= 0 {
		d.enrollmentMax = time.Minute
	}
}

func (d *Daemon) configSnapshot() Config {
	d.configMu.RLock()
	defer d.configMu.RUnlock()
	return d.Config
}

func (d *Daemon) refreshConfig() error {
	if d.ConfigPath == "" {
		return nil
	}
	config, err := LoadConfig(d.ConfigPath)
	if err != nil {
		return err
	}
	d.configMu.Lock()
	defer d.configMu.Unlock()
	if d.Config.HostID != "" && config.HostID != d.Config.HostID {
		return errors.New("agent host identity changed while daemon was running")
	}
	d.Config = config
	return nil
}

func (d *Daemon) saveRegistrationState(state string) error {
	d.configMu.Lock()
	defer d.configMu.Unlock()
	config := d.Config
	config.RegistrationState = state
	if d.ConfigPath != "" {
		if err := SaveConfig(d.ConfigPath, config); err != nil {
			return err
		}
	}
	d.Config = config
	return nil
}

func (d *Daemon) enrollmentLoop(ctx context.Context) {
	retryDelay := d.enrollmentMin
	delay := time.Duration(0)
	for {
		if !d.waitForEnrollment(ctx, delay) {
			return
		}
		if err := d.refreshConfig(); err != nil {
			delay = d.enrollmentPoll
			continue
		}
		switch d.configSnapshot().RegistrationState {
		case RegistrationActive:
			d.discardSetupTokenForState(RegistrationActive)
			retryDelay = d.enrollmentMin
			delay = d.enrollmentPoll
		case RegistrationNeedsSetupToken:
			d.discardSetupTokenForState(RegistrationNeedsSetupToken)
			retryDelay = d.enrollmentMin
			delay = d.enrollmentPoll
		case RegistrationPending:
			switch d.attemptEnrollment(ctx) {
			case enrollmentSucceeded:
				retryDelay = d.enrollmentMin
				delay = d.enrollmentPoll
			case enrollmentWaiting:
				retryDelay = d.enrollmentMin
				delay = d.enrollmentPoll
			case enrollmentRetry:
				delay = retryDelay
				retryDelay *= 2
				if retryDelay > d.enrollmentMax {
					retryDelay = d.enrollmentMax
				}
			}
		default:
			delay = d.enrollmentPoll
		}
	}
}

func (d *Daemon) waitForEnrollment(ctx context.Context, delay time.Duration) bool {
	if delay <= 0 {
		return ctx.Err() == nil
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

func (d *Daemon) attemptEnrollment(ctx context.Context) enrollmentResult {
	unlock, err := LockEnrollment(d.EnrollmentLock)
	if err != nil {
		return enrollmentRetry
	}
	defer unlock()
	if err := d.refreshConfig(); err != nil {
		return enrollmentRetry
	}
	if d.SetupTokenPath == "" {
		_ = d.saveRegistrationState(RegistrationNeedsSetupToken)
		return enrollmentWaiting
	}
	token, err := ReadSetupTokenFile(d.SetupTokenPath)
	if err != nil {
		if d.saveRegistrationState(RegistrationNeedsSetupToken) == nil {
			d.discardSetupToken()
		}
		return enrollmentWaiting
	}
	config := d.configSnapshot()
	requestContext, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	err = RegisterHost(requestContext, d.Client, config.Endpoint, token, config.HostID, d.PrivateKey, d.Version, d.now())
	if err == nil {
		if d.saveRegistrationState(RegistrationActive) != nil {
			return enrollmentRetry
		}
		d.discardSetupToken()
		select {
		case d.wake <- struct{}{}:
		default:
		}
		return enrollmentSucceeded
	}
	var registrationError *RegistrationError
	if errors.As(err, &registrationError) && registrationError.Permanent() {
		if d.saveRegistrationState(RegistrationNeedsSetupToken) != nil {
			return enrollmentRetry
		}
		d.discardSetupToken()
		return enrollmentWaiting
	}
	return enrollmentRetry
}

func (d *Daemon) discardSetupToken() {
	if d.SetupTokenPath == "" {
		return
	}
	if err := os.Remove(d.SetupTokenPath); err != nil && !os.IsNotExist(err) {
		return
	}
}

func (d *Daemon) discardSetupTokenForState(expectedState string) {
	unlock, err := LockEnrollment(d.EnrollmentLock)
	if err != nil {
		return
	}
	defer unlock()
	if err := d.refreshConfig(); err != nil {
		return
	}
	if d.configSnapshot().RegistrationState == expectedState {
		d.discardSetupToken()
	}
}
