// Copyright 2016 Keybase Inc. All rights reserved.
// Use of this source code is governed by a BSD
// license that can be found in the LICENSE file.

package libkbfs

import (
	"errors"
	"fmt"
	"sync"

	"github.com/keybase/client/go/kbfs/libcontext"
	"github.com/keybase/client/go/kbfs/tlf"
	kbname "github.com/keybase/client/go/kbun"
	"github.com/keybase/client/go/libkb"
	"github.com/keybase/client/go/protocol/keybase1"
	"golang.org/x/net/context"
	"golang.org/x/sync/errgroup"
)

type extendedIdentify struct {
	behavior keybase1.TLFIdentifyBehavior

	// lock guards userBreaks and tlfBreaks
	lock       sync.Mutex
	userBreaks chan keybase1.TLFIdentifyFailure
	tlfBreaks  *keybase1.TLFBreak
}

func (ei *extendedIdentify) userBreak(
	ctx context.Context, username kbname.NormalizedUsername, uid keybase1.UID,
	breaks *keybase1.IdentifyTrackBreaks) {
	if ei.userBreaks == nil {
		return
	}

	select {
	case ei.userBreaks <- keybase1.TLFIdentifyFailure{
		Breaks: breaks,
		User: keybase1.User{
			Uid:      uid,
			Username: string(username),
		},
	}:
	case <-ctx.Done():
	}
}

func (ei *extendedIdentify) teamBreak(
	ctx context.Context, teamID keybase1.TeamID,
	breaks *keybase1.IdentifyTrackBreaks) {
	if ei.userBreaks == nil {
		return
	}

	if breaks != nil && (len(breaks.Keys) != 0 || len(breaks.Proofs) != 0) {
		panic(fmt.Sprintf("Unexpected team %s breaks: %v", teamID, breaks))
	}

	// Otherwise just send an empty message to close the loop.
	select {
	case ei.userBreaks <- keybase1.TLFIdentifyFailure{
		Breaks: nil,
		User:   keybase1.User{},
	}:
	case <-ctx.Done():
	}
}

func (ei *extendedIdentify) onError(ctx context.Context) {
	if ei.userBreaks == nil {
		return
	}

	// The identify got an error, so just send a nil breaks list so
	// that the goroutine waiting on the breaks can finish and the
	// error can be returned.
	select {
	case ei.userBreaks <- keybase1.TLFIdentifyFailure{
		Breaks: nil,
		User:   keybase1.User{},
	}:
	case <-ctx.Done():
	}
}

func (ei *extendedIdentify) makeTlfBreaksIfNeeded(
	ctx context.Context, numUserInTlf int) error {
	if ei.userBreaks == nil {
		return nil
	}

	ei.lock.Lock()
	defer ei.lock.Unlock()

	b := &keybase1.TLFBreak{}
	for i := 0; i < numUserInTlf; i++ {
		select {
		case ub, ok := <-ei.userBreaks:
			if !ok {
				return errors.New("makeTlfBreaksIfNeeded called on extendedIdentify" +
					" with closed userBreaks channel.")
			}
			if ub.Breaks != nil {
				b.Breaks = append(b.Breaks, ub)
			}
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	ei.tlfBreaks = b

	return nil
}

// getTlfBreakOrBust returns a keybase1.TLFBreak. This should only be called
// for behavior.WarningInsteadOfErrorOnBrokenTracks() == true, and after
// makeTlfBreaksIfNeeded is called, to make sure user proof breaks get
// populated in GUI mode.
//
// If called otherwise, we don't panic here anymore, since we can't panic on
// nil ei.tlfBreaks. The reason is if a previous successful identify has
// already happened recently, it could cause this identify to be skipped, which
// means ei.tlfBreaks is never populated. In this case, it's safe to return an
// empty keybase1.TLFBreak.
func (ei *extendedIdentify) getTlfBreakAndClose() keybase1.TLFBreak {
	ei.lock.Lock()
	defer ei.lock.Unlock()

	if ei.userBreaks != nil {
		close(ei.userBreaks)
		ei.userBreaks = nil
	}
	if ei.tlfBreaks != nil {
		return *ei.tlfBreaks
	}

	return keybase1.TLFBreak{}
}

// ctxExtendedIdentifyKeyType is a type for the context key for using
// extendedIdentify
type ctxExtendedIdentifyKeyType int

const (
	// ctxExtendedIdentifyKeyType is a context key for using extendedIdentify
	ctxExtendedIdentifyKey ctxExtendedIdentifyKeyType = iota
)

// ExtendedIdentifyAlreadyExists is returned when MakeExtendedIdentify is
// called on a context already with extendedIdentify.
type ExtendedIdentifyAlreadyExists struct{}

func (e ExtendedIdentifyAlreadyExists) Error() string {
	return "extendedIdentify already exists"
}

// MakeExtendedIdentify populates a context with an extendedIdentify directive.
func MakeExtendedIdentify(ctx context.Context,
	behavior keybase1.TLFIdentifyBehavior) (context.Context, error) {
	if _, ok := ctx.Value(ctxExtendedIdentifyKey).(*extendedIdentify); ok {
		return nil, ExtendedIdentifyAlreadyExists{}
	}

	if !behavior.WarningInsteadOfErrorOnBrokenTracks() {
		return libcontext.NewContextReplayable(ctx, func(ctx context.Context) context.Context {
			return context.WithValue(ctx, ctxExtendedIdentifyKey, &extendedIdentify{
				behavior: behavior,
			})
		}), nil
	}

	ch := make(chan keybase1.TLFIdentifyFailure)
	return libcontext.NewContextReplayable(ctx, func(ctx context.Context) context.Context {
		return context.WithValue(ctx, ctxExtendedIdentifyKey, &extendedIdentify{
			behavior:   behavior,
			userBreaks: ch,
		})
	}), nil
}

func getExtendedIdentify(ctx context.Context) (ei *extendedIdentify) {
	if ei, ok := ctx.Value(ctxExtendedIdentifyKey).(*extendedIdentify); ok {
		return ei
	}
	return &extendedIdentify{
		behavior: keybase1.TLFIdentifyBehavior_DEFAULT_KBFS,
	}
}

// identifyUID performs identify based only on UID. It should be
// used only if the username is not known - as e.g. when rekeying.
func identifyUID(ctx context.Context, nug normalizedUsernameGetter,
	identifier identifier, id keybase1.UserOrTeamID, t tlf.Type,
	offline keybase1.OfflineAvailability) error {
	name, err := nug.GetNormalizedUsername(ctx, id, offline)
	if err != nil {
		return err
	}
	return identifyUser(ctx, nug, identifier, name, id, t, offline)
}

// identifyUser is the preferred way to run identifies.
func identifyUser(ctx context.Context, nug normalizedUsernameGetter,
	identifier identifier, name kbname.NormalizedUsername,
	id keybase1.UserOrTeamID, t tlf.Type,
	offline keybase1.OfflineAvailability) error {
	// Check to see if identify should be skipped altogether.
	ei := getExtendedIdentify(ctx)
	if ei.behavior == keybase1.TLFIdentifyBehavior_CHAT_SKIP {
		return nil
	}

	var reason string
	nameAssertion := name.String()
	isImplicit := false
	switch t {
	case tlf.Public:
		if id.IsTeam() {
			isImplicit = true
		}
		reason = "You accessed a public folder."
	case tlf.Private:
		if id.IsTeam() {
			isImplicit = true
			reason = fmt.Sprintf(
				"You accessed a folder for private team %s.", nameAssertion)
		} else {
			reason = fmt.Sprintf(
				"You accessed a private folder with %s.", nameAssertion)
		}
	case tlf.SingleTeam:
		reason = fmt.Sprintf(
			"You accessed a folder for private team %s.", nameAssertion)
		nameAssertion = "team:" + nameAssertion
	}
	var resultName kbname.NormalizedUsername
	var resultID keybase1.UserOrTeamID
	if isImplicit {
		assertions, extensionSuffix, err := tlf.SplitExtension(name.String())
		if err != nil {
			return err
		}
		iteamInfo, err := identifier.IdentifyImplicitTeam(
			ctx, assertions, extensionSuffix, t, reason, offline)
		if err != nil {
			return err
		}
		resultName = iteamInfo.Name
		resultID = iteamInfo.TID.AsUserOrTeam()
	} else {
		var err error
		resultName, resultID, err =
			identifier.Identify(ctx, nameAssertion, reason, offline)
		if err != nil {
			// Convert libkb.NoSigChainError into one we can report.  (See
			// KBFS-1252).
			if _, ok := err.(libkb.NoSigChainError); ok {
				return NoSigChainError{name}
			}
			return err
		}
	}
	// The names of implicit teams can change out from under us,
	// unlike for regular users, so don't require that they remain the
	// same.
	if resultName != name && !isImplicit {
		return fmt.Errorf("Identify returned name=%s, expected %s",
			resultName, name)
	}
	if resultID != id {
		return fmt.Errorf("Identify returned uid=%s, expected %s", resultID, id)
	}
	return nil
}

// identifyUserToChan calls identifyUser and plugs the result into the error channnel.
func identifyUserToChan(ctx context.Context, nug normalizedUsernameGetter,
	identifier identifier, name kbname.NormalizedUsername,
	id keybase1.UserOrTeamID, t tlf.Type, offline keybase1.OfflineAvailability,
	errChan chan error) {
	errChan <- identifyUser(ctx, nug, identifier, name, id, t, offline)
}

// identifyUsers identifies the users in the given maps.
func identifyUsers(ctx context.Context, nug normalizedUsernameGetter,
	identifier identifier,
	names map[keybase1.UserOrTeamID]kbname.NormalizedUsername,
	t tlf.Type, offline keybase1.OfflineAvailability) error {
	eg, ctx := errgroup.WithContext(ctx)

	// TODO: limit the number of concurrent identifies?
	// TODO: implement a version of errgroup with limited concurrency.
	for id, name := range names {
		// Capture range variables.
		id, name := id, name
		eg.Go(func() error {
			return identifyUser(ctx, nug, identifier, name, id, t, offline)
		})
	}

	return eg.Wait()
}

// identifyUserList identifies the users in the given list.
// Only use this when the usernames are not known - like when rekeying.
func identifyUserList(ctx context.Context, nug normalizedUsernameGetter,
	identifier identifier, ids []keybase1.UserOrTeamID, t tlf.Type,
	offline keybase1.OfflineAvailability) error {
	eg, ctx := errgroup.WithContext(ctx)

	// TODO: limit the number of concurrent identifies?
	// TODO: implement concurrency limited version of errgroup.
	for _, id := range ids {
		// Capture range variable.
		id := id
		eg.Go(func() error {
			return identifyUID(
				ctx, nug, identifier, id, t, offline)
		})
	}

	return eg.Wait()
}

// identifyUsersForTLF is a helper for identifyHandle for easier testing.
func identifyUsersForTLF(ctx context.Context, nug normalizedUsernameGetter,
	identifier identifier,
	names map[keybase1.UserOrTeamID]kbname.NormalizedUsername,
	t tlf.Type, offline keybase1.OfflineAvailability) error {
	ei := getExtendedIdentify(ctx)
	if ei.behavior == keybase1.TLFIdentifyBehavior_CHAT_SKIP {
		return nil
	}

	eg, ctx := errgroup.WithContext(ctx)

	eg.Go(func() error {
		return ei.makeTlfBreaksIfNeeded(ctx, len(names))
	})

	eg.Go(func() error {
		return identifyUsers(ctx, nug, identifier, names, t, offline)
	})

	return eg.Wait()
}

// identifyHandle identifies the canonical names in the given handle.
func identifyHandle(
	ctx context.Context, nug normalizedUsernameGetter, identifier identifier,
	osg OfflineStatusGetter, h *TlfHandle) error {
	offline := keybase1.OfflineAvailability_NONE
	if osg != nil {
		offline = osg.OfflineAvailabilityForID(h.tlfID)
	}
	return identifyUsersForTLF(
		ctx, nug, identifier, h.ResolvedUsersMap(), h.Type(), offline)
}
