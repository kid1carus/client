// Copyright 2015 Keybase, Inc. All rights reserved. Use of
// this source code is governed by the included BSD license.

package libkb

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	keybase1 "github.com/keybase/client/go/protocol/keybase1"
	jsonw "github.com/keybase/go-jsonw"
)

const (
	DBUser              = 0x00
	DBSig               = 0x0f
	DBTeamChain         = 0x10
	DBUserPlusAllKeysV1 = 0x19

	DBOfflineRPC               = 0xbe
	DBChatCollapses            = 0xbf
	DBMerkleAudit              = 0xca
	DBUnfurler                 = 0xcb
	DBStellarDisclaimer        = 0xcc
	DBFTLStorage               = 0xcd
	DBTeamAuditor              = 0xce
	DBAttachmentUploader       = 0xcf
	DBHasRandomPW              = 0xd0
	DBDiskLRUEntries           = 0xda
	DBDiskLRUIndex             = 0xdb
	DBImplicitTeamConflictInfo = 0xdc
	DBUidToFullName            = 0xdd
	DBUidToUsername            = 0xde
	DBUserPlusKeysVersioned    = 0xdf
	DBLink                     = 0xe0
	DBLocalTrack               = 0xe1
	DBPGPKey                   = 0xe3
	DBSigHints                 = 0xe4
	DBProofCheck               = 0xe5
	DBUserSecretKeys           = 0xe6
	DBSigChainTailPublic       = 0xe7
	DBSigChainTailSemiprivate  = 0xe8
	DBSigChainTailEncrypted    = 0xe9
	DBChatActive               = 0xea
	DBUserEKBox                = 0xeb
	DBTeamEKBox                = 0xec
	DBChatIndex                = 0xed
	DBChatReacji               = 0xef
	DBMerkleRoot               = 0xf0
	DBTrackers                 = 0xf1
	DBGregor                   = 0xf2
	DBTrackers2                = 0xf3
	DBTrackers2Reverse         = 0xf4
	DBNotificationDismiss      = 0xf5
	DBChatBlockIndex           = 0xf6
	DBChatBlocks               = 0xf7
	DBChatOutbox               = 0xf8
	DBChatInbox                = 0xf9
	DBIdentify                 = 0xfa
	DBResolveUsernameToUID     = 0xfb
	DBChatBodyHashIndex        = 0xfc
	DBMerkleStore              = 0xfd
	DBChatConvFailures         = 0xfe
	DBTeamList                 = 0xff
)

const (
	DBLookupUsername   = 0x00
	DBLookupMerkleRoot = 0x01
)

func DbKeyUID(t ObjType, uid keybase1.UID) DbKey {
	return DbKey{Typ: t, Key: uid.String()}
}

func DbKeyNotificationDismiss(prefix string, username NormalizedUsername) DbKey {
	return DbKey{
		Typ: DBNotificationDismiss,
		Key: fmt.Sprintf("%s:%s", prefix, username),
	}
}

// IsPermDbKey returns true for keys ignored by the leveldb cleaner and always
// persisted to disk.  Ideally these keys handling some cleanup/size bounding
// themselves.
func IsPermDbKey(typ ObjType) bool {
	switch typ {
	case DBDiskLRUEntries,
		DBDiskLRUIndex,
		DBOfflineRPC,
		DBChatCollapses,
		DBHasRandomPW,
		DBChatReacji,
		DBStellarDisclaimer:
		return true
	default:
		return false
	}
}

type ObjType byte

type DbKey struct {
	Typ ObjType
	Key string
}

// tablePrefix builds a key prefix for the given table for use in `util.Range`
// or `util.BytesPrefix`
func tablePrefix(table string) []byte {
	return []byte(fmt.Sprintf("%s:", table))
}

func (k DbKey) ToString(table string) string {
	return fmt.Sprintf("%s:%02x:%s", table, k.Typ, k.Key)
}

func (k DbKey) ToBytes(table string) []byte {
	if IsPermDbKey(k.Typ) {
		table = levelDbTablePerm
	}
	return []byte(k.ToString(table))
}

var fieldExp = regexp.MustCompile(`[a-f0-9]{2}`)

func DbKeyParse(s string) (string, DbKey, error) {
	v := strings.Split(s, ":")
	if len(v) < 3 {
		return "", DbKey{}, fmt.Errorf("expected 3 colon-separated fields, found %d", len(v))
	}

	if !fieldExp.MatchString(v[1]) {
		return "", DbKey{}, fmt.Errorf("2nd field should be a 1-byte hex string")
	}

	b, err := strconv.ParseUint(v[1], 16, 8)
	if err != nil {
		return "", DbKey{}, err
	}
	dbKey := DbKey{
		Typ: ObjType(b),
		Key: strings.Join(v[2:], ":"),
	}
	return v[0], dbKey, nil
}

func jsonLocalDbPut(ops LocalDbOps, id DbKey, aliases []DbKey, val *jsonw.Wrapper) error {
	bytes, err := val.Marshal()
	if err == nil {
		err = ops.Put(id, aliases, bytes)
	}
	return err
}
func jsonLocalDbGet(ops LocalDbOps, id DbKey) (*jsonw.Wrapper, error) {
	bytes, found, err := ops.Get(id)
	var ret *jsonw.Wrapper
	if found {
		ret, err = jsonw.Unmarshal(bytes)
	}
	return ret, err
}

func jsonLocalDbGetInto(ops LocalDbOps, obj interface{}, id DbKey) (found bool, err error) {
	var buf []byte
	buf, found, err = ops.Get(id)
	if err == nil && found {
		err = jsonw.EnsureMaxDepthBytesDefault(buf)
		if err != nil {
			return found, err
		}
		err = json.Unmarshal(buf, &obj)
	}
	return found, err
}

func jsonLocalDbPutObj(ops LocalDbOps, id DbKey, aliases []DbKey, obj interface{}) (err error) {
	var bytes []byte
	bytes, err = json.Marshal(obj)
	if err == nil {
		err = ops.Put(id, aliases, bytes)
	}
	return err
}

func jsonLocalDbLookup(ops LocalDbOps, id DbKey) (*jsonw.Wrapper, error) {
	bytes, found, err := ops.Lookup(id)
	var ret *jsonw.Wrapper
	if found {
		ret, err = jsonw.Unmarshal(bytes)
	}
	return ret, err
}

func jsonLocalDbGetIntoMsgpack(ops LocalDbOps, obj interface{}, id DbKey) (found bool, err error) {
	var buf []byte
	buf, found, err = ops.Get(id)
	if err != nil || !found {
		return found, err
	}
	err = MsgpackDecode(obj, buf)
	return true, err
}

func jsonLocalDbPutObjMsgpack(ops LocalDbOps, id DbKey, aliases []DbKey, obj interface{}) error {
	bytes, err := MsgpackEncode(obj)
	if err != nil {
		return err
	}
	return ops.Put(id, aliases, bytes)
}

type JSONLocalDb struct {
	engine LocalDb
}

func NewJSONLocalDb(e LocalDb) *JSONLocalDb   { return &JSONLocalDb{e} }
func (j *JSONLocalDb) Open() error            { return j.engine.Open() }
func (j *JSONLocalDb) ForceOpen() error       { return j.engine.ForceOpen() }
func (j *JSONLocalDb) Close() error           { return j.engine.Close() }
func (j *JSONLocalDb) Nuke() (string, error)  { return j.engine.Nuke() }
func (j *JSONLocalDb) Clean(force bool) error { return j.engine.Clean(force) }
func (j *JSONLocalDb) Stats() string          { return j.engine.Stats() }

func (j *JSONLocalDb) PutRaw(id DbKey, b []byte) error       { return j.engine.Put(id, nil, b) }
func (j *JSONLocalDb) GetRaw(id DbKey) ([]byte, bool, error) { return j.engine.Get(id) }
func (j *JSONLocalDb) Delete(id DbKey) error                 { return j.engine.Delete(id) }

func (j *JSONLocalDb) Put(id DbKey, aliases []DbKey, val *jsonw.Wrapper) error {
	return jsonLocalDbPut(j.engine, id, aliases, val)
}

func (j *JSONLocalDb) Get(id DbKey) (*jsonw.Wrapper, error) {
	return jsonLocalDbGet(j.engine, id)
}

func (j *JSONLocalDb) GetInto(obj interface{}, id DbKey) (found bool, err error) {
	return jsonLocalDbGetInto(j.engine, obj, id)
}

func (j *JSONLocalDb) PutObj(id DbKey, aliases []DbKey, obj interface{}) (err error) {
	return jsonLocalDbPutObj(j.engine, id, aliases, obj)
}

func (j *JSONLocalDb) Lookup(id DbKey) (*jsonw.Wrapper, error) {
	return jsonLocalDbLookup(j.engine, id)
}

func (j *JSONLocalDb) GetIntoMsgpack(obj interface{}, id DbKey) (found bool, err error) {
	return jsonLocalDbGetIntoMsgpack(j.engine, obj, id)
}

func (j *JSONLocalDb) PutObjMsgpack(id DbKey, aliases []DbKey, obj interface{}) (err error) {
	return jsonLocalDbPutObjMsgpack(j.engine, id, aliases, obj)
}

func (j *JSONLocalDb) OpenTransaction() (JSONLocalDbTransaction, error) {
	var (
		jtr JSONLocalDbTransaction
		err error
	)
	if jtr.tr, err = j.engine.OpenTransaction(); err != nil {
		return JSONLocalDbTransaction{}, err
	}
	return jtr, nil
}

func (j *JSONLocalDb) GetEngine() LocalDb {
	return j.engine
}

type JSONLocalDbTransaction struct {
	tr LocalDbTransaction
}

func (j JSONLocalDbTransaction) PutRaw(id DbKey, b []byte) error       { return j.tr.Put(id, nil, b) }
func (j JSONLocalDbTransaction) GetRaw(id DbKey) ([]byte, bool, error) { return j.tr.Get(id) }
func (j JSONLocalDbTransaction) Delete(id DbKey) error                 { return j.tr.Delete(id) }

func (j JSONLocalDbTransaction) Put(id DbKey, aliases []DbKey, val *jsonw.Wrapper) error {
	return jsonLocalDbPut(j.tr, id, aliases, val)
}

func (j JSONLocalDbTransaction) Get(id DbKey) (*jsonw.Wrapper, error) {
	return jsonLocalDbGet(j.tr, id)
}

func (j JSONLocalDbTransaction) GetInto(obj interface{}, id DbKey) (found bool, err error) {
	return jsonLocalDbGetInto(j.tr, obj, id)
}

func (j JSONLocalDbTransaction) PutObj(id DbKey, aliases []DbKey, obj interface{}) (err error) {
	return jsonLocalDbPutObj(j.tr, id, aliases, obj)
}

func (j JSONLocalDbTransaction) Lookup(id DbKey) (*jsonw.Wrapper, error) {
	return jsonLocalDbLookup(j.tr, id)
}

func (j JSONLocalDbTransaction) Commit() error {
	return j.tr.Commit()
}

func (j JSONLocalDbTransaction) Discard() {
	j.tr.Discard()
}
