package libkb

import (
	"errors"
)

func ResetAccount(m MetaContext, username NormalizedUsername, passphrase string) (err error) {
	defer m.Trace("ResetAccount", func() error { return err })()
	return resetOrDeleteAccount(m, username, passphrase, "nuke")
}

func DeleteAccount(m MetaContext, username NormalizedUsername, passphrase string) (err error) {
	defer m.Trace("DeleteAccount", func() error { return err })()
	return resetOrDeleteAccount(m, username, passphrase, "delete")
}

func resetOrDeleteAccount(m MetaContext, username NormalizedUsername, passphrase string, endpoint string) (err error) {
	defer m.Trace("resetOrDeleteAccount", func() error { return err })()

	m = m.WithNewProvisionalLoginContext()
	err = PassphraseLoginNoPrompt(m, username.String(), passphrase)
	if err != nil {
		return err
	}
	pps := m.PassphraseStream()
	if pps == nil {
		return errors.New("unexpected nil passphrase stream")
	}

	pdpka, err := ComputeLoginPackage2(m, pps)
	if err != nil {
		return err
	}

	arg := APIArg{
		Endpoint:    endpoint,
		SessionType: APISessionTypeREQUIRED,
		Args:        NewHTTPArgs(),
	}
	pdpka.PopulateArgs(&arg.Args)
	_, err = m.G().API.Post(m, arg)
	return err
}
