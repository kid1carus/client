// Copyright 2016 Keybase Inc. All rights reserved.
// Use of this source code is governed by a BSD
// license that can be found in the LICENSE file.

package libkbfs

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/keybase/client/go/kbfs/ioutil"
	"github.com/keybase/client/go/kbfs/kbfsblock"
	"github.com/keybase/client/go/kbfs/kbfscrypto"
	"github.com/keybase/client/go/kbfs/kbfsmd"
	"github.com/keybase/client/go/kbfs/tlf"
	"github.com/keybase/client/go/logger"
	"github.com/keybase/client/go/protocol/keybase1"
	"github.com/pkg/errors"
	"golang.org/x/net/context"
	"golang.org/x/sync/errgroup"
)

type journalManagerConfig struct {
	// EnableAuto, if true, means the user has explicitly set its
	// value. If false, then either the user turned it on and then
	// off, or the user hasn't turned it on at all.
	EnableAuto bool

	// EnableAutoSetByUser means the user has explicitly set the
	// value of EnableAuto (after this field was added).
	EnableAutoSetByUser bool
}

func (jsc journalManagerConfig) getEnableAuto(currentUID keybase1.UID) (
	enableAuto, enableAutoSetByUser bool) {
	// If EnableAuto is true, the user has explicitly set its value.
	if jsc.EnableAuto {
		return true, true
	}

	// Otherwise, if EnableAutoSetByUser is true, it means the
	// user has explicitly set the value of EnableAuto (after that
	// field was added).
	if jsc.EnableAutoSetByUser {
		return false, true
	}

	// Otherwise, if the user hasn't explicitly turned off journaling,
	// it's enabled by default.
	return true, false
}

// JournalManagerStatus represents the overall status of the
// JournalManager for display in diagnostics. It is suitable for
// encoding directly as JSON.
type JournalManagerStatus struct {
	RootDir             string
	Version             int
	CurrentUID          keybase1.UID
	CurrentVerifyingKey kbfscrypto.VerifyingKey
	EnableAuto          bool
	EnableAutoSetByUser bool
	JournalCount        int
	// The byte counters below are signed because
	// os.FileInfo.Size() is signed. The file counter is signed
	// for consistency.
	StoredBytes       int64
	StoredFiles       int64
	UnflushedBytes    int64
	UnflushedPaths    []string
	EndEstimate       *time.Time
	DiskLimiterStatus interface{}
}

// branchChangeListener describes a caller that will get updates via
// the onTLFBranchChange method call when the journal branch changes
// for the given TlfID.  If a new branch has been created, the given
// kbfsmd.BranchID will be something other than kbfsmd.NullBranchID.  If the current
// branch was pruned, it will be kbfsmd.NullBranchID.  If the implementer
// will be accessing the journal, it must do so from another goroutine
// to avoid deadlocks.
type branchChangeListener interface {
	onTLFBranchChange(tlf.ID, kbfsmd.BranchID)
}

// mdFlushListener describes a caller that will ge updates via the
// onMDFlush metod when an MD is flushed.  If the implementer will be
// accessing the journal, it must do so from another goroutine to
// avoid deadlocks.
type mdFlushListener interface {
	onMDFlush(tlf.ID, kbfsmd.BranchID, kbfsmd.Revision)
}

// JournalManager is the server that handles write journals. It
// interposes itself in front of BlockServer and MDOps. It uses MDOps
// instead of MDServer because it has to potentially modify the
// RootMetadata passed in, and by the time it hits MDServer it's
// already too late. However, this assumes that all MD ops go through
// MDOps.
//
// The maximum number of characters added to the root dir by a journal
// server journal is 108: 51 for the TLF journal, and 57 for
// everything else.
//
//   /v1/de...-...(53 characters total)...ff(/tlf journal)
type JournalManager struct {
	config Config

	log      traceLogger
	deferLog traceLogger

	dir string

	delegateBlockCache      BlockCache
	delegateDirtyBlockCache DirtyBlockCache
	delegateBlockServer     BlockServer
	delegateMDOps           MDOps
	onBranchChange          branchChangeListener
	onMDFlush               mdFlushListener

	// Just protects lastQuotaError.
	lastQuotaErrorLock sync.Mutex
	lastQuotaError     time.Time

	// Just protects lastDiskLimitError.
	lastDiskLimitErrorLock sync.Mutex
	lastDiskLimitError     time.Time

	// Protects all fields below.
	lock                sync.RWMutex
	currentUID          keybase1.UID
	currentVerifyingKey kbfscrypto.VerifyingKey
	tlfJournals         map[tlf.ID]*tlfJournal
	dirtyOps            map[tlf.ID]uint
	dirtyOpsDone        *sync.Cond
	serverConfig        journalManagerConfig
}

func makeJournalManager(
	config Config, log logger.Logger, dir string,
	bcache BlockCache, dirtyBcache DirtyBlockCache, bserver BlockServer,
	mdOps MDOps, onBranchChange branchChangeListener,
	onMDFlush mdFlushListener) *JournalManager {
	if len(dir) == 0 {
		panic("journal root path string unexpectedly empty")
	}
	jManager := JournalManager{
		config:                  config,
		log:                     traceLogger{log},
		deferLog:                traceLogger{log.CloneWithAddedDepth(1)},
		dir:                     dir,
		delegateBlockCache:      bcache,
		delegateDirtyBlockCache: dirtyBcache,
		delegateBlockServer:     bserver,
		delegateMDOps:           mdOps,
		onBranchChange:          onBranchChange,
		onMDFlush:               onMDFlush,
		tlfJournals:             make(map[tlf.ID]*tlfJournal),
		dirtyOps:                make(map[tlf.ID]uint),
	}
	jManager.dirtyOpsDone = sync.NewCond(&jManager.lock)
	return &jManager
}

func (j *JournalManager) rootPath() string {
	return filepath.Join(j.dir, "v1")
}

func (j *JournalManager) configPath() string {
	return filepath.Join(j.rootPath(), "config.json")
}

func (j *JournalManager) readConfig() error {
	return ioutil.DeserializeFromJSONFile(j.configPath(), &j.serverConfig)
}

func (j *JournalManager) writeConfig() error {
	return ioutil.SerializeToJSONFile(j.serverConfig, j.configPath())
}

func (j *JournalManager) tlfJournalPathLocked(tlfID tlf.ID) string {
	if j.currentVerifyingKey == (kbfscrypto.VerifyingKey{}) {
		panic("currentVerifyingKey is zero")
	}

	// We need to generate a unique path for each (UID, device,
	// TLF) tuple. Verifying keys (which are unique to a device)
	// are globally unique, so no need to have the uid in the
	// path. Furthermore, everything after the first two bytes
	// (four characters) is randomly generated, so taking the
	// first 36 characters of the verifying key gives us 16 random
	// bytes (since the first two bytes encode version/type) or
	// 128 random bits, which means that the expected number of
	// devices generated before getting a collision in the first
	// part of the path is 2^64 (see
	// https://en.wikipedia.org/wiki/Birthday_problem#Cast_as_a_collision_problem
	// ).
	//
	// By similar reasoning, for a single device, taking the first
	// 16 characters of the TLF ID gives us 64 random bits, which
	// means that the expected number of TLFs associated to that
	// device before getting a collision in the second part of the
	// path is 2^32.
	shortDeviceIDStr := j.currentVerifyingKey.String()[:36]
	shortTlfIDStr := tlfID.String()[:16]
	dir := fmt.Sprintf("%s-%s", shortDeviceIDStr, shortTlfIDStr)
	return filepath.Join(j.rootPath(), dir)
}

func (j *JournalManager) getEnableAutoLocked() (
	enableAuto, enableAutoSetByUser bool) {
	return j.serverConfig.getEnableAuto(j.currentUID)
}

func (j *JournalManager) getTLFJournal(
	tlfID tlf.ID, h *TlfHandle) (*tlfJournal, bool) {
	getJournalFn := func() (*tlfJournal, bool, bool, bool) {
		j.lock.RLock()
		defer j.lock.RUnlock()
		// Don't create any journals when logged out.
		if j.currentUID.IsNil() {
			return nil, false, false, false
		}
		tlfJournal, ok := j.tlfJournals[tlfID]
		enableAuto, enableAutoSetByUser := j.getEnableAutoLocked()
		return tlfJournal, enableAuto, enableAutoSetByUser, ok
	}
	tlfJournal, enableAuto, enableAutoSetByUser, ok := getJournalFn()
	if !ok && enableAuto {
		ctx := context.TODO() // plumb through from callers

		if h == nil {
			// h must always be passed in for MD write operations, so
			// we are always safe in refusing new TLF journals in this
			// case.
			return nil, false
		}

		// Because of the above handle check, which will happen on
		// every put of a TLF, we will be able to create a journal on
		// the first write that happens after the user becomes a
		// writer for the TLF.
		isWriter, err := IsWriterFromHandle(
			ctx, h, j.config.KBPKI(), j.config, j.currentUID,
			j.currentVerifyingKey)
		if err != nil {
			j.log.CWarningf(ctx, "Couldn't find writership for %s: %+v",
				tlfID, err)
			return nil, false
		}
		if !isWriter {
			return nil, false
		}

		j.log.CDebugf(ctx, "Enabling a new journal for %s (enableAuto=%t, set by user=%t)",
			tlfID, enableAuto, enableAutoSetByUser)
		bws := TLFJournalBackgroundWorkEnabled
		if j.config.Mode().Type() == InitSingleOp {
			bws = TLFJournalSingleOpBackgroundWorkEnabled
		}
		err = j.Enable(ctx, tlfID, h, bws)
		if err != nil {
			j.log.CWarningf(ctx, "Couldn't enable journal for %s: %+v", tlfID, err)
			return nil, false
		}
		tlfJournal, _, _, ok = getJournalFn()
	}
	return tlfJournal, ok
}

func (j *JournalManager) hasTLFJournal(tlfID tlf.ID) bool {
	j.lock.RLock()
	defer j.lock.RUnlock()
	_, ok := j.tlfJournals[tlfID]
	return ok
}

func (j *JournalManager) makeFBOForJournal(
	ctx context.Context, tj *tlfJournal, tlfID tlf.ID) error {
	bid, err := tj.getBranchID()
	if err != nil {
		return err
	}

	head, err := tj.getMDHead(ctx, bid)
	if err != nil {
		return err
	}

	if head == (ImmutableBareRootMetadata{}) {
		return nil
	}

	headBareHandle, err := head.MakeBareTlfHandleWithExtra()
	if err != nil {
		return err
	}

	handle, err := MakeTlfHandle(
		ctx, headBareHandle, tlfID.Type(), j.config.KBPKI(),
		j.config.KBPKI(), constIDGetter{tlfID},
		j.config.OfflineAvailabilityForID(tlfID))
	if err != nil {
		return err
	}

	_, _, err = j.config.KBFSOps().GetRootNode(ctx, handle, MasterBranch)
	return err
}

// MakeFBOsForExistingJournals creates folderBranchOps objects for all
// existing, non-empty journals.  This is useful to initialize the
// unflushed edit history, for example.  It returns a wait group that
// the caller can use to determine when all the FBOs have been
// initialized.  If the caller is not going to wait on the group, it
// should provoide a context that won't be canceled before the wait
// group is finished.
func (j *JournalManager) MakeFBOsForExistingJournals(
	ctx context.Context) *sync.WaitGroup {
	var wg sync.WaitGroup

	j.lock.Lock()
	defer j.lock.Unlock()
	for tlfID, tj := range j.tlfJournals {
		wg.Add(1)
		tlfID := tlfID
		tj := tj
		go func() {
			ctx := CtxWithRandomIDReplayable(
				context.Background(), CtxFBOIDKey, CtxFBOOpID, j.log)

			// Turn off tracker popups.
			ctx, err := MakeExtendedIdentify(
				ctx, keybase1.TLFIdentifyBehavior_KBFS_INIT)
			if err != nil {
				j.log.CWarningf(ctx, "Error making extended identify: %+v", err)
			}

			defer wg.Done()
			j.log.CDebugf(ctx,
				"Initializing FBO for non-empty journal: %s", tlfID)

			err = j.makeFBOForJournal(ctx, tj, tlfID)
			if err != nil {
				j.log.CWarningf(ctx,
					"Error when making FBO for existing journal for %s: "+
						"%+v", tlfID, err)
			}

			// The popups and errors were suppressed, but any errors would
			// have been logged.  So just close out the extended identify.  If
			// the user accesses the TLF directly, another proper identify
			// should happen that shows errors.
			_ = getExtendedIdentify(ctx).getTlfBreakAndClose()
		}()
	}
	return &wg
}

// EnableExistingJournals turns on the write journal for all TLFs for
// the given (UID, device) tuple (with the device identified by its
// verifying key) with an existing journal. Any returned error means
// that the JournalManager remains in the same state as it was before.
//
// Once this is called, this must not be called again until
// shutdownExistingJournals is called.
func (j *JournalManager) EnableExistingJournals(
	ctx context.Context, currentUID keybase1.UID,
	currentVerifyingKey kbfscrypto.VerifyingKey,
	bws TLFJournalBackgroundWorkStatus) (err error) {
	j.log.CDebugf(ctx, "Enabling existing journals (%s)", bws)
	defer func() {
		if err != nil {
			j.deferLog.CDebugf(ctx,
				"Error when enabling existing journals: %+v",
				err)
		}
	}()

	if currentUID == keybase1.UID("") {
		return errors.New("Current UID is empty")
	}
	if currentVerifyingKey == (kbfscrypto.VerifyingKey{}) {
		return errors.New("Current verifying key is empty")
	}

	// TODO: We should also look up journals from other
	// users/devices so that we can take into account their
	// journal usage.

	j.lock.Lock()
	defer j.lock.Unlock()

	if j.currentUID == currentUID {
		// The user is not changing, so nothing needs to be done.
		return nil
	} else if j.currentUID != keybase1.UID("") {
		return errors.Errorf("Trying to set current UID from %s to %s",
			j.currentUID, currentUID)
	}
	if j.currentVerifyingKey != (kbfscrypto.VerifyingKey{}) {
		return errors.Errorf(
			"Trying to set current verifying key from %s to %s",
			j.currentVerifyingKey, currentVerifyingKey)
	}

	err = j.readConfig()
	switch {
	case ioutil.IsNotExist(err):
		// Config file doesn't exist, so write out a default one.
		err := j.writeConfig()
		if err != nil {
			return err
		}
	case err != nil:
		return err
	}

	// Need to set it here since tlfJournalPathLocked and
	// enableLocked depend on it.
	j.currentUID = currentUID
	j.currentVerifyingKey = currentVerifyingKey

	enableSucceeded := false
	defer func() {
		// Revert to a clean state if the enable doesn't
		// succeed, either due to a panic or error.
		if !enableSucceeded {
			j.shutdownExistingJournalsLocked(ctx)
		}
	}()

	fileInfos, err := ioutil.ReadDir(j.rootPath())
	if ioutil.IsNotExist(err) {
		enableSucceeded = true
		return nil
	} else if err != nil {
		return err
	}

	eg, groupCtx := errgroup.WithContext(ctx)

	fileCh := make(chan os.FileInfo, len(fileInfos))
	type journalRet struct {
		id      tlf.ID
		journal *tlfJournal
	}
	journalCh := make(chan journalRet, len(fileInfos))
	worker := func() error {
		for fi := range fileCh {
			name := fi.Name()
			if !fi.IsDir() {
				j.log.CDebugf(groupCtx, "Skipping file %q", name)
				continue
			}

			dir := filepath.Join(j.rootPath(), name)
			uid, key, tlfID, chargedTo, err := readTLFJournalInfoFile(dir)
			if err != nil {
				j.log.CDebugf(
					groupCtx, "Skipping non-TLF dir %q: %+v", name, err)
				continue
			}

			if uid != currentUID {
				j.log.CDebugf(
					groupCtx, "Skipping dir %q due to mismatched UID %s",
					name, uid)
				continue
			}

			if key != currentVerifyingKey {
				j.log.CDebugf(
					groupCtx, "Skipping dir %q due to mismatched key %s",
					name, uid)
				continue
			}

			expectedDir := j.tlfJournalPathLocked(tlfID)
			if dir != expectedDir {
				j.log.CDebugf(
					groupCtx, "Skipping misnamed dir %q; expected %q",
					dir, expectedDir)
				continue
			}

			// Allow enable even if dirty, since any dirty writes
			// in flight are most likely for another user.
			tj, err := j.enableLocked(groupCtx, tlfID, chargedTo, bws, true)
			if err != nil {
				// Don't treat per-TLF errors as fatal.
				j.log.CWarningf(
					groupCtx,
					"Error when enabling existing journal for %s: %+v",
					tlfID, err)
				continue
			}

			// Delete any empty journals so they don't clutter up the
			// directory, until the TLF is accessed again.
			blockEntryCount, mdEntryCount, err := tj.getJournalEntryCounts()
			if err != nil {
				tj.shutdown(groupCtx)
				// Don't treat per-TLF errors as fatal.
				j.log.CWarningf(
					groupCtx,
					"Error when getting status of existing journal for %s: %+v",
					tlfID, err)
				continue
			}
			if blockEntryCount == 0 && mdEntryCount == 0 {
				j.log.CDebugf(groupCtx, "Nuking empty journal for %s", tlfID)
				tj.shutdown(groupCtx)
				os.RemoveAll(dir)
				continue
			}

			journalCh <- journalRet{tlfID, tj}
		}
		return nil
	}

	// Initialize many TLF journals at once to overlap disk latency as
	// much as possible.
	numWorkers := 100
	if numWorkers > len(fileInfos) {
		numWorkers = len(fileInfos)
	}
	for i := 0; i < numWorkers; i++ {
		eg.Go(worker)
	}

	for _, fi := range fileInfos {
		fileCh <- fi
	}
	close(fileCh)

	err = eg.Wait()
	if err != nil {
		// None of the workers return an error so this should never
		// happen...
		return err
	}
	close(journalCh)

	for r := range journalCh {
		j.tlfJournals[r.id] = r.journal
	}

	j.log.CDebugf(ctx, "Done enabling journals")

	enableSucceeded = true
	return nil
}

// enabledLocked returns an enabled journal; it is the caller's
// responsibility to add it to `j.tlfJournals`.  This allows this
// method to be called in parallel during initialization, if desired.
func (j *JournalManager) enableLocked(
	ctx context.Context, tlfID tlf.ID, chargedTo keybase1.UserOrTeamID,
	bws TLFJournalBackgroundWorkStatus, allowEnableIfDirty bool) (
	tj *tlfJournal, err error) {
	j.log.CDebugf(ctx, "Enabling journal for %s (%s)", tlfID, bws)
	defer func() {
		if err != nil {
			j.deferLog.CDebugf(ctx,
				"Error when enabling journal for %s: %+v",
				tlfID, err)
		}
	}()

	if j.currentUID == keybase1.UID("") {
		return nil, errors.New("Current UID is empty")
	}
	if j.currentVerifyingKey == (kbfscrypto.VerifyingKey{}) {
		return nil, errors.New("Current verifying key is empty")
	}

	if tj, ok := j.tlfJournals[tlfID]; ok {
		err = tj.enable()
		if err != nil {
			return nil, err
		}
		return tj, nil
	}

	err = func() error {
		if j.dirtyOps[tlfID] > 0 {
			return errors.Errorf("Can't enable journal for %s while there "+
				"are outstanding dirty ops", tlfID)
		}
		if j.delegateDirtyBlockCache.IsAnyDirty(tlfID) {
			return errors.Errorf("Can't enable journal for %s while there "+
				"are any dirty blocks outstanding", tlfID)
		}
		return nil
	}()
	if err != nil {
		if !allowEnableIfDirty {
			return nil, err
		}

		j.log.CWarningf(ctx,
			"Got ignorable error on journal enable, and proceeding anyway: %+v",
			err)
	}

	tlfDir := j.tlfJournalPathLocked(tlfID)
	tj, err = makeTLFJournal(
		ctx, j.currentUID, j.currentVerifyingKey, tlfDir,
		tlfID, chargedTo, tlfJournalConfigAdapter{j.config},
		j.delegateBlockServer,
		bws, nil, j.onBranchChange, j.onMDFlush, j.config.DiskLimiter())
	if err != nil {
		return nil, err
	}

	return tj, nil
}

// Enable turns on the write journal for the given TLF.  If h is nil,
// it will be attempted to be fetched from the remote MD server.
func (j *JournalManager) Enable(ctx context.Context, tlfID tlf.ID,
	h *TlfHandle, bws TLFJournalBackgroundWorkStatus) (err error) {
	j.lock.Lock()
	defer j.lock.Unlock()
	chargedTo := j.currentUID.AsUserOrTeam()
	if tlfID.Type() == tlf.SingleTeam {
		if h == nil {
			// Any path that creates a single-team TLF journal should
			// also provide a handle.  If not, we'd have to fetch it
			// from the server, which isn't a trusted path.
			return errors.Errorf(
				"No handle provided for single-team TLF %s", tlfID)
		}

		chargedTo = h.FirstResolvedWriter()
		if tid := chargedTo.AsTeamOrBust(); tid.IsSubTeam() {
			// We can't charge to subteams; find the root team.
			rootID, err := j.config.KBPKI().GetTeamRootID(
				ctx, tid, j.config.OfflineAvailabilityForID(tlfID))
			if err != nil {
				return err
			}
			chargedTo = rootID.AsUserOrTeam()
		}
	}
	tj, err := j.enableLocked(ctx, tlfID, chargedTo, bws, false)
	if err != nil {
		return err
	}
	j.tlfJournals[tlfID] = tj
	return nil
}

// EnableAuto turns on the write journal for all TLFs, even new ones,
// persistently.
func (j *JournalManager) EnableAuto(ctx context.Context) error {
	j.lock.Lock()
	defer j.lock.Unlock()
	if j.serverConfig.EnableAuto {
		// Nothing to do.
		return nil
	}

	j.log.CDebugf(ctx, "Enabling auto-journaling")
	j.serverConfig.EnableAuto = true
	j.serverConfig.EnableAutoSetByUser = true
	return j.writeConfig()
}

// DisableAuto turns off automatic write journal for any
// newly-accessed TLFs.  Existing journaled TLFs need to be disabled
// manually.
func (j *JournalManager) DisableAuto(ctx context.Context) error {
	j.lock.Lock()
	defer j.lock.Unlock()
	if enabled, _ := j.getEnableAutoLocked(); !enabled {
		// Nothing to do.
		return nil
	}

	j.log.CDebugf(ctx, "Disabling auto-journaling")
	j.serverConfig.EnableAuto = false
	j.serverConfig.EnableAutoSetByUser = true
	return j.writeConfig()
}

func (j *JournalManager) dirtyOpStart(tlfID tlf.ID) {
	j.lock.Lock()
	defer j.lock.Unlock()
	j.dirtyOps[tlfID]++
}

func (j *JournalManager) dirtyOpEnd(tlfID tlf.ID) {
	j.lock.Lock()
	defer j.lock.Unlock()
	if j.dirtyOps[tlfID] == 0 {
		panic("Trying to end a dirty op when count is 0")
	}
	j.dirtyOps[tlfID]--
	if j.dirtyOps[tlfID] == 0 {
		delete(j.dirtyOps, tlfID)
	}
	if len(j.dirtyOps) == 0 {
		j.dirtyOpsDone.Broadcast()
	}
}

// PauseBackgroundWork pauses the background work goroutine, if it's
// not already paused.
func (j *JournalManager) PauseBackgroundWork(ctx context.Context, tlfID tlf.ID) {
	j.log.CDebugf(ctx, "Signaling pause for %s", tlfID)
	if tlfJournal, ok := j.getTLFJournal(tlfID, nil); ok {
		tlfJournal.pauseBackgroundWork()
		return
	}

	j.log.CDebugf(ctx,
		"Could not find journal for %s; dropping pause signal",
		tlfID)
}

// ResumeBackgroundWork resumes the background work goroutine, if it's
// not already resumed.
func (j *JournalManager) ResumeBackgroundWork(ctx context.Context, tlfID tlf.ID) {
	j.log.CDebugf(ctx, "Signaling resume for %s", tlfID)
	if tlfJournal, ok := j.getTLFJournal(tlfID, nil); ok {
		tlfJournal.resumeBackgroundWork()
		return
	}

	j.log.CDebugf(ctx,
		"Could not find journal for %s; dropping resume signal",
		tlfID)
}

// Flush flushes the write journal for the given TLF.
func (j *JournalManager) Flush(ctx context.Context, tlfID tlf.ID) (err error) {
	j.log.CDebugf(ctx, "Flushing journal for %s", tlfID)
	if tlfJournal, ok := j.getTLFJournal(tlfID, nil); ok {
		// TODO: do we want to plumb lc through here as well?
		return tlfJournal.flush(ctx)
	}

	j.log.CDebugf(ctx, "Journal not enabled for %s", tlfID)
	return nil
}

// Wait blocks until the write journal has finished flushing
// everything.  It is essentially the same as Flush() when the journal
// is enabled and unpaused, except that it is safe to cancel the
// context without leaving the journal in a partially-flushed state.
// It does not wait for any conflicts or squashes resulting from
// flushing the data currently in the journal.
func (j *JournalManager) Wait(ctx context.Context, tlfID tlf.ID) (err error) {
	j.log.CDebugf(ctx, "Waiting on journal for %s", tlfID)
	if tlfJournal, ok := j.getTLFJournal(tlfID, nil); ok {
		return tlfJournal.wait(ctx)
	}

	j.log.CDebugf(ctx, "Journal not enabled for %s", tlfID)
	return nil
}

// WaitForCompleteFlush blocks until the write journal has finished
// flushing everything.  Unlike `Wait()`, it also waits for any
// conflicts or squashes detected during each flush attempt.
func (j *JournalManager) WaitForCompleteFlush(
	ctx context.Context, tlfID tlf.ID) (err error) {
	j.log.CDebugf(ctx, "Finishing single op for %s", tlfID)
	if tlfJournal, ok := j.getTLFJournal(tlfID, nil); ok {
		return tlfJournal.waitForCompleteFlush(ctx)
	}

	j.log.CDebugf(ctx, "Journal not enabled for %s", tlfID)
	return nil
}

// FinishSingleOp lets the write journal know that the application has
// finished a single op, and then blocks until the write journal has
// finished flushing everything.
func (j *JournalManager) FinishSingleOp(ctx context.Context, tlfID tlf.ID,
	lc *keybase1.LockContext, priority keybase1.MDPriority) (err error) {
	j.log.CDebugf(ctx, "Finishing single op for %s", tlfID)
	if tlfJournal, ok := j.getTLFJournal(tlfID, nil); ok {
		return tlfJournal.finishSingleOp(ctx, lc, priority)
	}

	j.log.CDebugf(ctx, "Journal not enabled for %s", tlfID)
	return nil
}

// Disable turns off the write journal for the given TLF.
func (j *JournalManager) Disable(ctx context.Context, tlfID tlf.ID) (
	wasEnabled bool, err error) {
	j.log.CDebugf(ctx, "Disabling journal for %s", tlfID)
	defer func() {
		if err != nil {
			j.deferLog.CDebugf(ctx,
				"Error when disabling journal for %s: %+v",
				tlfID, err)
		}
	}()

	j.lock.Lock()
	defer j.lock.Unlock()
	tlfJournal, ok := j.tlfJournals[tlfID]
	if !ok {
		j.log.CDebugf(ctx, "Journal doesn't exist for %s", tlfID)
		return false, nil
	}

	if j.dirtyOps[tlfID] > 0 {
		return false, errors.Errorf("Can't disable journal for %s while there "+
			"are outstanding dirty ops", tlfID)
	}
	if j.delegateDirtyBlockCache.IsAnyDirty(tlfID) {
		return false, errors.Errorf("Can't disable journal for %s while there "+
			"are any dirty blocks outstanding", tlfID)
	}

	// Disable the journal.  Note that we don't bother deleting the
	// journal from j.tlfJournals, to avoid cases where something
	// keeps it around doing background work or re-enables it, at the
	// same time JournalManager creates a new journal for the same TLF.
	wasEnabled, err = tlfJournal.disable()
	if err != nil {
		return false, err
	}

	if wasEnabled {
		j.log.CDebugf(ctx, "Disabled journal for %s", tlfID)
	}
	return wasEnabled, nil
}

func (j *JournalManager) blockCache() journalBlockCache {
	return journalBlockCache{j, j.delegateBlockCache}
}

func (j *JournalManager) dirtyBlockCache(
	journalCache DirtyBlockCache) journalDirtyBlockCache {
	return journalDirtyBlockCache{j, j.delegateDirtyBlockCache, journalCache}
}

func (j *JournalManager) blockServer() journalBlockServer {
	return journalBlockServer{j, j.delegateBlockServer, false}
}

func (j *JournalManager) mdOps() journalMDOps {
	return journalMDOps{j.delegateMDOps, j}
}

func (j *JournalManager) maybeReturnOverQuotaError(
	usedQuotaBytes, quotaBytes int64) error {
	if usedQuotaBytes <= quotaBytes {
		return nil
	}

	j.lastQuotaErrorLock.Lock()
	defer j.lastQuotaErrorLock.Unlock()

	now := j.config.Clock().Now()
	// Return OverQuota errors only occasionally, so we don't spam
	// the keybase daemon with notifications. (See
	// PutBlockCheckQuota in block_util.go.)
	const overQuotaDuration = time.Minute
	if now.Sub(j.lastQuotaError) < overQuotaDuration {
		return nil
	}

	j.lastQuotaError = now
	return kbfsblock.ServerErrorOverQuota{
		Usage:     usedQuotaBytes,
		Limit:     quotaBytes,
		Throttled: false,
	}
}

func (j *JournalManager) maybeMakeDiskLimitErrorReportable(
	err *ErrDiskLimitTimeout) error {
	j.lastDiskLimitErrorLock.Lock()
	defer j.lastDiskLimitErrorLock.Unlock()

	now := j.config.Clock().Now()
	// Return DiskLimit errors only occasionally, so we don't spam
	// the keybase daemon with notifications. (See
	// PutBlockCheckLimitErrs in block_util.go.)
	const overDiskLimitDuration = time.Minute
	if now.Sub(j.lastDiskLimitError) < overDiskLimitDuration {
		return err
	}

	err.reportable = true
	j.lastDiskLimitError = now
	return err
}

// Status returns a JournalManagerStatus object suitable for
// diagnostics.  It also returns a list of TLF IDs which have journals
// enabled.
func (j *JournalManager) Status(
	ctx context.Context) (JournalManagerStatus, []tlf.ID) {
	j.lock.RLock()
	defer j.lock.RUnlock()
	var totalStoredBytes, totalStoredFiles, totalUnflushedBytes int64
	tlfIDs := make([]tlf.ID, 0, len(j.tlfJournals))
	for _, tlfJournal := range j.tlfJournals {
		storedBytes, storedFiles, unflushedBytes, err :=
			tlfJournal.getByteCounts()
		if err != nil {
			j.log.CWarningf(ctx,
				"Couldn't calculate stored bytes/stored files/unflushed bytes for %s: %+v",
				tlfJournal.tlfID, err)
		}
		totalStoredBytes += storedBytes
		totalStoredFiles += storedFiles
		totalUnflushedBytes += unflushedBytes
		tlfIDs = append(tlfIDs, tlfJournal.tlfID)
	}
	enableAuto, enableAutoSetByUser := j.getEnableAutoLocked()
	return JournalManagerStatus{
		RootDir:             j.rootPath(),
		Version:             1,
		CurrentUID:          j.currentUID,
		CurrentVerifyingKey: j.currentVerifyingKey,
		EnableAuto:          enableAuto,
		EnableAutoSetByUser: enableAutoSetByUser,
		JournalCount:        len(tlfIDs),
		StoredBytes:         totalStoredBytes,
		StoredFiles:         totalStoredFiles,
		UnflushedBytes:      totalUnflushedBytes,
		DiskLimiterStatus: j.config.DiskLimiter().getStatus(
			ctx, j.currentUID.AsUserOrTeam()),
	}, tlfIDs
}

// JournalStatus returns a TLFServerStatus object for the given TLF
// suitable for diagnostics.
func (j *JournalManager) JournalStatus(tlfID tlf.ID) (
	TLFJournalStatus, error) {
	tlfJournal, ok := j.getTLFJournal(tlfID, nil)
	if !ok {
		return TLFJournalStatus{},
			errors.Errorf("Journal not enabled for %s", tlfID)
	}

	return tlfJournal.getJournalStatus()
}

// JournalStatusWithPaths returns a TLFServerStatus object for the
// given TLF suitable for diagnostics, including paths for all the
// unflushed entries.
func (j *JournalManager) JournalStatusWithPaths(ctx context.Context,
	tlfID tlf.ID, cpp chainsPathPopulator) (TLFJournalStatus, error) {
	tlfJournal, ok := j.getTLFJournal(tlfID, nil)
	if !ok {
		return TLFJournalStatus{},
			errors.Errorf("Journal not enabled for %s", tlfID)
	}

	return tlfJournal.getJournalStatusWithPaths(ctx, cpp)
}

// shutdownExistingJournalsLocked shuts down all write journals, sets
// the current UID and verifying key to zero, and returns once all
// shutdowns are complete. It is safe to call multiple times in a row,
// and once this is called, EnableExistingJournals may be called
// again.
func (j *JournalManager) shutdownExistingJournalsLocked(ctx context.Context) {
	for len(j.dirtyOps) > 0 {
		j.log.CDebugf(ctx,
			"Waiting for %d TLFS with dirty ops before shutting down "+
				"existing journals...", len(j.dirtyOps))
		j.dirtyOpsDone.Wait()
	}

	j.log.CDebugf(ctx, "Shutting down existing journals")

	for _, tlfJournal := range j.tlfJournals {
		tlfJournal.shutdown(ctx)
	}

	j.tlfJournals = make(map[tlf.ID]*tlfJournal)
	j.currentUID = keybase1.UID("")
	j.currentVerifyingKey = kbfscrypto.VerifyingKey{}
}

// shutdownExistingJournals shuts down all write journals, sets the
// current UID and verifying key to zero, and returns once all
// shutdowns are complete. It is safe to call multiple times in a row,
// and once this is called, EnableExistingJournals may be called
// again.
func (j *JournalManager) shutdownExistingJournals(ctx context.Context) {
	j.lock.Lock()
	defer j.lock.Unlock()
	j.shutdownExistingJournalsLocked(ctx)
}

func (j *JournalManager) shutdown(ctx context.Context) {
	j.log.CDebugf(ctx, "Shutting down journal")
	j.lock.Lock()
	defer j.lock.Unlock()
	for _, tlfJournal := range j.tlfJournals {
		tlfJournal.shutdown(ctx)
	}

	// Leave all the tlfJournals in j.tlfJournals, so that any
	// access to them errors out instead of mutating the journal.
}
