package ephemeral

import (
	"context"
	"fmt"
	"io/ioutil"
	"net/url"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/keybase/client/go/engine"
	"github.com/keybase/client/go/erasablekv"
	"github.com/keybase/client/go/libkb"
	"github.com/keybase/client/go/protocol/keybase1"
	"github.com/keybase/clockwork"
	"github.com/stretchr/testify/require"
)

func getNoiseFilePath(tc libkb.TestContext, key string) string {
	noiseName := fmt.Sprintf("%s.ns", url.QueryEscape(key))
	return filepath.Join(tc.G.Env.GetDataDir(), "eraseablekvstore", "device-eks", noiseName)
}

func TestKeygenIfNeeded(t *testing.T) {
	tc, _ := ephemeralKeyTestSetup(t)
	defer tc.Cleanup()

	ekLib := NewEKLib(tc.G)
	defer ekLib.Shutdown()
	deviceEKStorage := tc.G.GetDeviceEKStorage()
	userEKBoxStorage := tc.G.GetUserEKBoxStorage()

	expectedDeviceEKGen, err := deviceEKStorage.MaxGeneration(context.Background(), false)
	require.NoError(t, err)
	if expectedDeviceEKGen < 0 {
		expectedDeviceEKGen = 1
		deviceEKNeeded, err := ekLib.NewDeviceEKNeeded(context.Background())
		require.NoError(t, err)
		require.True(t, deviceEKNeeded)
	}

	expectedUserEKGen, err := userEKBoxStorage.MaxGeneration(context.Background(), false)
	require.NoError(t, err)
	if expectedUserEKGen < 0 {
		expectedUserEKGen = 1
		userEKNeeded, err := ekLib.NewUserEKNeeded(context.Background())
		require.NoError(t, err)
		require.True(t, userEKNeeded)
	}

	keygen := func(expectedDeviceEKGen, expectedUserEKGen keybase1.EkGeneration) {
		err := ekLib.KeygenIfNeeded(context.Background())
		require.NoError(t, err)

		// verify deviceEK
		deviceEKNeeded, err := ekLib.NewDeviceEKNeeded(context.Background())
		require.NoError(t, err)
		require.False(t, deviceEKNeeded)

		deviceEKMaxGen, err := deviceEKStorage.MaxGeneration(context.Background(), false)
		require.NoError(t, err)
		require.Equal(t, expectedDeviceEKGen, deviceEKMaxGen)

		// verify userEK
		userEKNeeded, err := ekLib.NewUserEKNeeded(context.Background())
		require.NoError(t, err)
		require.False(t, userEKNeeded)

		userEKMaxGen, err := userEKBoxStorage.MaxGeneration(context.Background(), false)
		require.NoError(t, err)
		require.Equal(t, expectedUserEKGen, userEKMaxGen)
	}

	// If we retry keygen, we don't regenerate keys
	t.Logf("Initial keygen")
	keygen(expectedDeviceEKGen, expectedUserEKGen)
	t.Logf("Keygen again does not create new keys")
	keygen(expectedDeviceEKGen, expectedUserEKGen)

	rawDeviceEKStorage := NewDeviceEKStorage(tc.G)
	rawUserEKBoxStorage := NewUserEKBoxStorage(tc.G)

	// Let's purge our local userEK store and make sure we don't regenerate
	// (respecting the server max)
	err = rawUserEKBoxStorage.Delete(context.Background(), expectedUserEKGen)
	require.NoError(t, err)
	userEKBoxStorage.ClearCache()
	keygen(expectedDeviceEKGen, expectedUserEKGen)

	// Now let's kill our deviceEK as well by deleting the noise file, we
	// should regenerate a new userEK since we can't access the old one
	key, err := rawDeviceEKStorage.key(context.Background(), expectedDeviceEKGen)
	require.NoError(t, err)
	noiseFilePath := getNoiseFilePath(tc, key)
	err = os.Remove(noiseFilePath)
	require.NoError(t, err)

	deviceEKStorage.ClearCache()
	expectedDeviceEKGen++
	expectedUserEKGen++
	t.Logf("Keygen with corrupted deviceEK works")
	keygen(expectedDeviceEKGen, expectedUserEKGen)

	// Test ForceDeleteAll
	err = deviceEKStorage.ForceDeleteAll(context.Background(), tc.G.Env.GetUsername())
	require.NoError(t, err)
	deviceEKs, err := rawDeviceEKStorage.GetAll(context.Background())
	require.NoError(t, err)
	require.Len(t, deviceEKs, 0)
}

func TestNewTeamEKNeeded(t *testing.T) {
	tc, _ := ephemeralKeyTestSetup(t)
	defer tc.Cleanup()

	teamID := createTeam(tc)
	ekLib := NewEKLib(tc.G)
	defer ekLib.Shutdown()
	fc := clockwork.NewFakeClockAt(time.Now())
	ekLib.setClock(fc)
	deviceEKStorage := tc.G.GetDeviceEKStorage()
	userEKBoxStorage := tc.G.GetUserEKBoxStorage()
	teamEKBoxStorage := tc.G.GetTeamEKBoxStorage()

	// We don't have any keys, so we should need a new teamEK
	needed, err := ekLib.NewTeamEKNeeded(context.Background(), teamID)
	require.NoError(t, err)
	require.True(t, needed)

	expectedTeamEKGen, err := teamEKBoxStorage.MaxGeneration(context.Background(), teamID, false)
	require.NoError(t, err)
	if expectedTeamEKGen < 0 {
		expectedTeamEKGen = 1
	}

	expectedDeviceEKGen, err := deviceEKStorage.MaxGeneration(context.Background(), false)
	require.NoError(t, err)
	if expectedDeviceEKGen < 0 {
		expectedDeviceEKGen = 1
	}

	expectedUserEKGen, err := userEKBoxStorage.MaxGeneration(context.Background(), false)
	require.NoError(t, err)
	if expectedUserEKGen < 0 {
		expectedUserEKGen = 1
	}

	assertKeyGenerations := func(expectedDeviceEKGen, expectedUserEKGen, expectedTeamEKGen keybase1.EkGeneration, shouldCreate, teamEKCreationInProgress bool) {
		teamEK, created, err := ekLib.GetOrCreateLatestTeamEK(context.Background(), teamID)
		require.NoError(t, err)
		require.Equal(t, shouldCreate, created)

		// verify the ekLib teamEKGenCache is working
		cacheKey := ekLib.cacheKey(teamID)
		val, ok := ekLib.teamEKGenCache.Get(cacheKey)
		require.True(t, ok)
		cacheEntry, expired := ekLib.isEntryExpired(val)
		require.False(t, expired)
		require.NotNil(t, cacheEntry)
		require.Equal(t, teamEKCreationInProgress, cacheEntry.CreationInProgress)
		require.Equal(t, teamEK.Metadata.Generation, cacheEntry.Generation)

		// verify deviceEK
		deviceEKNeeded, err := ekLib.NewDeviceEKNeeded(context.Background())
		require.NoError(t, err)
		require.False(t, deviceEKNeeded)

		deviceEKMaxGen, err := deviceEKStorage.MaxGeneration(context.Background(), false)
		require.NoError(t, err)
		require.Equal(t, expectedDeviceEKGen, deviceEKMaxGen)

		// verify userEK
		userEKNeeded, err := ekLib.NewUserEKNeeded(context.Background())
		require.NoError(t, err)
		require.False(t, userEKNeeded)

		userEKMaxGen, err := userEKBoxStorage.MaxGeneration(context.Background(), false)
		require.NoError(t, err)
		require.Equal(t, expectedUserEKGen, userEKMaxGen)

		// verify teamEK
		teamEKGen, err := teamEKBoxStorage.MaxGeneration(context.Background(), teamID, false)
		require.NoError(t, err)
		require.Equal(t, expectedTeamEKGen, teamEKGen)
		require.Equal(t, expectedTeamEKGen, teamEK.Metadata.Generation)

		teamEKNeeded, err := ekLib.NewTeamEKNeeded(context.Background(), teamID)
		require.NoError(t, err)
		require.False(t, teamEKNeeded)
	}

	assertKeyGenerations(expectedDeviceEKGen, expectedUserEKGen, expectedTeamEKGen, true /*created*/, false /* teamEKCreationInProgress */)
	// If we retry keygen, we don't regenerate keys
	assertKeyGenerations(expectedDeviceEKGen, expectedUserEKGen, expectedTeamEKGen, false /*created*/, false /* teamEKCreationInProgress */)

	rawDeviceEKStorage := NewDeviceEKStorage(tc.G)
	rawUserEKBoxStorage := NewUserEKBoxStorage(tc.G)
	rawTeamEKBoxStorage := NewTeamEKBoxStorage(tc.G)

	// Let's purge our local teamEK store and make sure we don't regenerate
	// (respecting the server max)
	err = rawTeamEKBoxStorage.Delete(context.Background(), teamID, expectedTeamEKGen)
	require.NoError(t, err)
	teamEKBoxStorage.ClearCache()
	assertKeyGenerations(expectedDeviceEKGen, expectedUserEKGen, expectedTeamEKGen, false /*created */, false /* teamEKCreationInProgress */)

	// Now let's kill our userEK, we should gracefully not regenerate
	// since we can still fetch the userEK from the server.
	err = rawUserEKBoxStorage.Delete(context.Background(), expectedUserEKGen)
	require.NoError(t, err)
	tc.G.GetDeviceEKStorage().ClearCache()
	assertKeyGenerations(expectedDeviceEKGen, expectedUserEKGen, expectedTeamEKGen, false /*created*/, false /* teamEKCreationInProgress */)

	// Now let's kill our deviceEK as well, and we should generate all new keys
	err = rawDeviceEKStorage.Delete(context.Background(), expectedDeviceEKGen)
	require.NoError(t, err)
	tc.G.GetDeviceEKStorage().ClearCache()
	expectedDeviceEKGen++
	expectedUserEKGen++
	expectedTeamEKGen++
	assertKeyGenerations(expectedDeviceEKGen, expectedUserEKGen, expectedTeamEKGen, true /*created*/, false /* teamEKCreationInProgress */)

	// If we try to access an older teamEK that we cannot access, we don't
	// create a new teamEK
	teamEK, err := ekLib.GetTeamEK(context.Background(), teamID, expectedTeamEKGen-1, nil)
	require.Error(t, err)
	require.IsType(t, EphemeralKeyError{}, err)
	ekErr := err.(EphemeralKeyError)
	require.Equal(t, DefaultHumanErrMsg, ekErr.HumanError())
	require.Equal(t, teamEK, keybase1.TeamEk{})
	assertKeyGenerations(expectedDeviceEKGen, expectedUserEKGen, expectedTeamEKGen, false /*created*/, false /* teamEKCreationInProgress */)

	// Now let's kill our deviceEK by corrupting a single bit in the noiseFile,
	// so we can no longer access the latest teamEK and will generate a new one
	// and verify it is the new valid max.
	key, err := rawDeviceEKStorage.key(context.Background(), expectedDeviceEKGen)
	require.NoError(t, err)
	noiseFilePath := getNoiseFilePath(tc, key)
	noise, err := ioutil.ReadFile(noiseFilePath)
	require.NoError(t, err)

	// flip one bit
	corruptedNoise := make([]byte, len(noise))
	copy(corruptedNoise, noise)
	corruptedNoise[0] ^= 0x01

	err = ioutil.WriteFile(noiseFilePath, corruptedNoise, libkb.PermFile)
	require.NoError(t, err)
	tc.G.GetDeviceEKStorage().ClearCache()

	ch := make(chan bool, 1)
	ekLib.setBackgroundCreationTestCh(ch)
	teamEK, err = ekLib.GetTeamEK(context.Background(), teamID, expectedTeamEKGen, nil)
	require.Error(t, err)
	require.IsType(t, EphemeralKeyError{}, err)
	ekErr = err.(EphemeralKeyError)
	require.Equal(t, DefaultHumanErrMsg, ekErr.HumanError())
	require.Equal(t, teamEK, keybase1.TeamEk{})
	t.Logf("before expectedTeamEkGen: %v", expectedTeamEKGen)
	select {
	case created := <-ch:
		require.True(t, created)
	case <-time.After(time.Second * 20):
		t.Fatalf("teamEK background creation failed")
	}

	expectedDeviceEKGen++
	expectedUserEKGen++
	expectedTeamEKGen++
	t.Logf("after expectedTeamEkGen: %v", expectedTeamEKGen)
	assertKeyGenerations(expectedDeviceEKGen, expectedUserEKGen, expectedTeamEKGen, false /*created*/, false /* teamEKCreationInProgress */)

	// Fake the teamEK creation time so we are forced to generate a new one.
	forceEKCtime := func(generation keybase1.EkGeneration, d time.Duration) {
		rawTeamEKBoxStorage.Get(context.Background(), teamID, generation, nil)
		cache, found, err := rawTeamEKBoxStorage.getCacheForTeamID(context.Background(), teamID)
		require.NoError(t, err)
		require.True(t, found)
		cacheItem, ok := cache[generation]
		require.True(t, ok)
		require.False(t, cacheItem.HasError())
		teamEKBoxed := cacheItem.TeamEKBoxed
		teamEKBoxed.Metadata.Ctime = keybase1.ToTime(teamEKBoxed.Metadata.Ctime.Time().Add(d))
		err = teamEKBoxStorage.Put(context.Background(), teamID, generation, teamEKBoxed)
		require.NoError(t, err)
	}

	// First we ensure that we don't do background generation for expired teamEKs.
	fc.Advance(cacheEntryLifetime) // expire our cache
	forceEKCtime(expectedTeamEKGen, -libkb.EphemeralKeyGenInterval)
	expectedTeamEKGen++
	assertKeyGenerations(expectedDeviceEKGen, expectedUserEKGen, expectedTeamEKGen, true /*created*/, false /* teamEKCreationInProgress */)

	// If we are *almost* expired, background generation is possible.
	fc.Advance(cacheEntryLifetime) // expire our cache
	forceEKCtime(expectedTeamEKGen, -libkb.EphemeralKeyGenInterval+30*time.Minute)
	assertKeyGenerations(expectedDeviceEKGen, expectedUserEKGen, expectedTeamEKGen, false /*created*/, true /* teamEKCreationInProgress */)
	assertKeyGenerations(expectedDeviceEKGen, expectedUserEKGen, expectedTeamEKGen, false /*created*/, true /* teamEKCreationInProgress */)
	// Signal background generation should start
	ch <- true

	// Wait until background generation completes
	select {
	case created := <-ch:
		require.True(t, created)
	case <-time.After(time.Second * 20):
		t.Fatalf("teamEK background creation failed")
	}
	close(ch)
	expectedTeamEKGen++
	assertKeyGenerations(expectedDeviceEKGen, expectedUserEKGen, expectedTeamEKGen, false /*created*/, false /* teamEKCreationInProgress */)
}

func TestCleanupStaleUserAndDeviceEKs(t *testing.T) {
	tc, _ := ephemeralKeyTestSetup(t)
	defer tc.Cleanup()

	seed, err := newDeviceEphemeralSeed()
	require.NoError(t, err)
	s := tc.G.GetDeviceEKStorage()
	ctimeExpired := time.Now().Add(-libkb.MaxEphemeralKeyStaleness * 3)
	err = s.Put(context.Background(), 0, keybase1.DeviceEk{
		Seed: keybase1.Bytes32(seed),
		Metadata: keybase1.DeviceEkMetadata{
			Ctime: keybase1.ToTime(ctimeExpired),
		},
	})
	require.NoError(t, err)

	ekLib := NewEKLib(tc.G)
	defer ekLib.Shutdown()
	err = ekLib.CleanupStaleUserAndDeviceEKs(context.Background())
	require.NoError(t, err)

	deviceEK, err := s.Get(context.Background(), 0)
	require.Error(t, err)
	_, ok := err.(erasablekv.UnboxError)
	require.True(t, ok)
	require.Equal(t, keybase1.DeviceEk{}, deviceEK)

	err = ekLib.CleanupStaleUserAndDeviceEKs(context.Background())
	require.NoError(t, err)
}

func TestCleanupStaleUserAndDeviceEKsOffline(t *testing.T) {
	tc, _ := ephemeralKeyTestSetup(t)
	defer tc.Cleanup()

	seed, err := newDeviceEphemeralSeed()
	require.NoError(t, err)
	s := tc.G.GetDeviceEKStorage()
	ctimeExpired := time.Now().Add(-libkb.MaxEphemeralKeyStaleness * 3)
	err = s.Put(context.Background(), 0, keybase1.DeviceEk{
		Seed: keybase1.Bytes32(seed),
		Metadata: keybase1.DeviceEkMetadata{
			Ctime:       keybase1.ToTime(ctimeExpired),
			DeviceCtime: keybase1.ToTime(ctimeExpired),
		},
	})
	require.NoError(t, err)

	ekLib := NewEKLib(tc.G)
	defer ekLib.Shutdown()
	ch := make(chan bool, 1)
	ekLib.setBackgroundDeleteTestCh(ch)
	err = ekLib.keygenIfNeeded(context.Background(), libkb.MerkleRoot{}, true /* shouldCleanup */)
	require.Error(t, err)
	_, ok := err.(EphemeralKeyError)
	require.False(t, ok)
	require.Equal(t, SkipKeygenNilMerkleRoot, err.Error())

	// Even though we return an error, we charge through on the deletion
	// successfully.
	select {
	case <-ch:
		deviceEK, err := s.Get(context.Background(), 0)
		require.Error(t, err)
		_, ok = err.(erasablekv.UnboxError)
		require.True(t, ok)
		require.Equal(t, keybase1.DeviceEk{}, deviceEK)
	}
	err = ekLib.keygenIfNeeded(context.Background(), libkb.MerkleRoot{}, true /* shouldCleanup */)
	require.Error(t, err)
	_, ok = err.(erasablekv.UnboxError)
	require.False(t, ok)
	require.Equal(t, SkipKeygenNilMerkleRoot, err.Error())
}

func TestLoginOneshotNoEphemeral(t *testing.T) {
	tc, user := ephemeralKeyTestSetup(t)
	defer tc.Cleanup()
	uis := libkb.UIs{
		LogUI:    tc.G.UI.GetLogUI(),
		LoginUI:  &libkb.TestLoginUI{RevokeBackup: false},
		SecretUI: &libkb.TestSecretUI{},
	}
	m := libkb.NewMetaContextForTest(tc).WithUIs(uis)
	teamID := createTeam(tc)

	ekLib := NewEKLib(tc.G)
	defer ekLib.Shutdown()
	teamEK, created, err := ekLib.GetOrCreateLatestTeamEK(context.Background(), teamID)
	require.NoError(t, err)
	require.True(t, created)

	eng := engine.NewPaperKey(tc.G)
	err = engine.RunEngine2(m, eng)
	require.NoError(t, err)
	require.NotZero(t, len(eng.Passphrase()))
	require.NoError(t, tc.G.Logout(context.TODO()))

	tc2 := libkb.SetupTest(t, "ephemeral", 2)
	defer tc2.Cleanup()
	m2 := libkb.NewMetaContextForTest(tc2)
	NewEphemeralStorageAndInstall(tc2.G)

	eng2 := engine.NewLoginOneshot(tc2.G, keybase1.LoginOneshotArg{
		Username: user.NormalizedUsername().String(),
		PaperKey: eng.Passphrase(),
	})
	err = engine.RunEngine2(m2, eng2)
	require.NoError(t, err)

	ekLib2 := NewEKLib(tc2.G)
	defer ekLib2.Shutdown()

	// Make sure we can't access or create any ephemeral keys
	teamEK, created, err = ekLib2.GetOrCreateLatestTeamEK(context.Background(), teamID)
	require.Error(t, err)
	require.False(t, created)
	_, ok := err.(EphemeralKeyError)
	require.False(t, ok)
	require.Equal(t, keybase1.TeamEk{}, teamEK)

	deks := tc2.G.GetDeviceEKStorage()
	gen, err := deks.MaxGeneration(context.Background(), false)
	require.NoError(t, err)
	require.EqualValues(t, -1, gen)

	ueks := tc2.G.GetUserEKBoxStorage()
	gen, err = ueks.MaxGeneration(context.Background(), false)
	require.NoError(t, err)
	require.EqualValues(t, -1, gen)

	teks := tc2.G.GetUserEKBoxStorage()
	gen, err = teks.MaxGeneration(context.Background(), false)
	require.NoError(t, err)
	require.EqualValues(t, -1, gen)
}
