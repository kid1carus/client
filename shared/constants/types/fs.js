// @flow
import * as I from 'immutable'
import * as RPCTypes from './rpc-gen'
import * as ChatTypes from './chat2'
import * as Devices from './devices'
import * as TeamsTypes from '../../constants/types/teams'
import * as FsGen from '../../actions/fs-gen'
import type {IconType} from '../../common-adapters/icon.constants'
import {type TextType} from '../../common-adapters/text'
import {isWindows} from '../platform'
import {memoize} from '../../util/memoize'
// lets not create cycles in flow, lets discuss how to fix this
// import {type Actions} from '../../actions/fs-gen'

export opaque type Path = ?string

export type PathType = 'folder' | 'file' | 'symlink' | 'unknown'
export type ProgressType = 'favorite' | 'pending' | 'loaded'

// not naming Error because it has meaning in js.
export type _FsError = {
  time: number,
  error: string,
  erroredAction: FsGen.Actions,
  retriableAction?: ?FsGen.Actions,
}
export type FsError = I.RecordOf<_FsError>

export type Device = {
  type: Devices.DeviceType,
  name: string,
  deviceID: string,
}

export type ParticipantUnlock = {
  name: string,
  devices: string,
}

export type ResetMember = {
  username: string,
  uid: string,
}

// TODO: make structs above immutable

export type TlfType = 'private' | 'public' | 'team'

export type _Tlf = {
  name: string,
  isFavorite: boolean,
  isIgnored: boolean,
  isNew: boolean,
  needsRekey: boolean,
  resetParticipants: I.List<ResetMember>,
  teamId: RPCTypes.TeamID,
  // Following two fields are calculated but not in-use today yet.
  //
  // waitingForParticipantUnlock is the list of participants that can unlock
  // this folder, when this folder needs a rekey.
  waitingForParticipantUnlock?: I.List<ParticipantUnlock>,
  // youCanUnlock has a list of devices that can unlock this folder, when this
  // folder needs a rekey.
  youCanUnlock?: I.List<Device>,
}
export type Tlf = I.RecordOf<_Tlf>

export type _ParsedPathRoot = {
  kind: 'root',
}
export type ParsedPathRoot = I.RecordOf<_ParsedPathRoot>

export type _ParsedPathTlfList = {
  kind: 'tlf-list',
  tlfType: TlfType,
}
export type ParsedPathTlfList = I.RecordOf<_ParsedPathTlfList>

export type _ParsedPathGroupTlf = {
  kind: 'group-tlf',
  tlfType: 'private' | 'public',
  writers: I.List<string>,
  readers: ?I.List<string>,
}
export type ParsedPathGroupTlf = I.RecordOf<_ParsedPathGroupTlf>

export type _ParsedPathTeamTlf = {
  kind: 'team-tlf',
  tlfType: 'team',
  team: string,
}
export type ParsedPathTeamTlf = I.RecordOf<_ParsedPathTeamTlf>

export type _ParsedPathInGroupTlf = {
  kind: 'in-group-tlf',
  tlfType: 'private' | 'public',
  writers: I.List<string>,
  readers: ?I.List<string>,
  rest: I.List<string>,
}
export type ParsedPathInGroupTlf = I.RecordOf<_ParsedPathInGroupTlf>

export type _ParsedPathInTeamTlf = {
  kind: 'in-team-tlf',
  tlfType: 'team',
  team: string,
  rest: I.List<string>,
}
export type ParsedPathInTeamTlf = I.RecordOf<_ParsedPathInTeamTlf>

export type ParsedPath =
  | ParsedPathRoot
  | ParsedPathTlfList
  | ParsedPathGroupTlf
  | ParsedPathTeamTlf
  | ParsedPathInGroupTlf
  | ParsedPathInTeamTlf

// name -> Tlf
export type TlfList = I.Map<string, Tlf>

export type _Tlfs = {
  private: TlfList,
  public: TlfList,
  team: TlfList,
}
export type Tlfs = I.RecordOf<_Tlfs>

export type PathItemMetadata = {
  name: string,
  lastModifiedTimestamp: number,
  size: number,
  lastWriter: RPCTypes.User,
  writable: boolean,
}

export type _FolderPathItem = {
  type: 'folder',
  children: I.Set<string>,
  progress: ProgressType,
} & PathItemMetadata
export type FolderPathItem = I.RecordOf<_FolderPathItem>

export type _SymlinkPathItem = {
  type: 'symlink',
  linkTarget: string,
} & PathItemMetadata
export type SymlinkPathItem = I.RecordOf<_SymlinkPathItem>

export type _Mime = {
  mimeType: string,
  displayPreview: boolean,
}
export type Mime = I.RecordOf<_Mime>

export type _FilePathItem = {
  type: 'file',
  mimeType: ?Mime,
} & PathItemMetadata
export type FilePathItem = I.RecordOf<_FilePathItem>

export type _UnknownPathItem = {
  type: 'unknown',
} & PathItemMetadata
export type UnknownPathItem = I.RecordOf<_UnknownPathItem>

export type PathItem = FolderPathItem | SymlinkPathItem | FilePathItem | UnknownPathItem

export opaque type EditID = string
export type EditType = 'new-folder'
export type EditStatusType = 'editing' | 'saving' | 'failed'

export type _NewFolder = {
  type: 'new-folder',
  parentPath: Path,
  name: string,
  hint: string,
  status: EditStatusType,
}
export type NewFolder = I.RecordOf<_NewFolder>

export type Edit = NewFolder

export type SortBy = 'name' | 'time'
export type SortOrder = 'asc' | 'desc'
export type _SortSetting = {
  sortBy: SortBy,
  sortOrder: SortOrder,
}
export type SortSetting = I.RecordOf<_SortSetting>

export type _PathUserSetting = {
  sort: SortSetting,
}
export type PathUserSetting = I.RecordOf<_PathUserSetting>

export type LocalPath = string

export type DownloadIntentMobile = 'camera-roll' | 'share'
export type DownloadIntent = 'none' | DownloadIntentMobile

export type _DownloadMeta = {
  entryType: PathType,
  intent: DownloadIntent,
  path: Path,
  localPath: LocalPath,
  opID: RPCTypes.OpID,
}
export type DownloadMeta = I.RecordOf<_DownloadMeta>

export type _DownloadState = {
  canceled: boolean,
  completePortion: number,
  endEstimate?: number,
  error?: FsError,
  isDone: boolean,
  startedAt: number,
}
export type DownloadState = I.RecordOf<_DownloadState>

export type _Download = {
  meta: DownloadMeta,
  state: DownloadState,
}
export type Download = I.RecordOf<_Download>

export type Downloads = I.Map<string, Download>

export type _Uploads = {
  writingToJournal: I.Set<Path>,
  errors: I.Map<Path, FsError>,

  totalSyncingBytes: number,
  endEstimate?: number,
  syncingPaths: I.Set<Path>,
}
export type Uploads = I.RecordOf<_Uploads>

// 'both' is only supported on macOS
export type OpenDialogType = 'file' | 'directory' | 'both'
export type MobilePickType = 'photo' | 'video' | 'mixed'

export type _LocalHTTPServer = {
  address: string,
  token: string,
}
export type LocalHTTPServer = I.RecordOf<_LocalHTTPServer>

export type FileEditType = 'created' | 'modified' | 'deleted' | 'renamed' | 'unknown'

export type _TlfEdit = {
  filename: string,
  serverTime: number,
  editType: FileEditType,
}

export type TlfEdit = I.RecordOf<_TlfEdit>

export type _TlfUpdate = {
  path: Path,
  writer: string,
  serverTime: number,
  history: I.List<TlfEdit>,
}

export type TlfUpdate = I.RecordOf<_TlfUpdate>

export type UserTlfUpdates = I.List<TlfUpdate>

export type PathItems = I.Map<Path, PathItem>

export type Edits = I.Map<EditID, Edit>

export type _MoveOrCopySource = {
  type: 'move-or-copy',
  path: Path,
}
export type MoveOrCopySource = I.RecordOf<_MoveOrCopySource>

export type _IncomingShareSource = {
  type: 'incoming-share',
  localPath: LocalPath,
}

export type IncomingShareSource = I.RecordOf<_IncomingShareSource>

export type _NoSource = {
  type: 'none',
}

export type NoSource = I.RecordOf<_NoSource>

export type _DestinationPicker = {
  // id -> Path mapping. This is useful for mobile when we have multiple layers
  // stacked on top of each other, and we need to keep track of them for the
  // back button. We don't put this in routeProps directly as that'd
  // complicate stuff for desktop because we don't have something like a
  // routeToSibling.
  destinationParentPath: I.List<Path>,
  source: MoveOrCopySource | IncomingShareSource | NoSource,
}

export type DestinationPicker = I.RecordOf<_DestinationPicker>

export type _SendAttachmentToChat = {
  filter: string,
  path: Path,
  convID: ChatTypes.ConversationIDKey,
}
export type SendAttachmentToChat = I.RecordOf<_SendAttachmentToChat>

export type _SendLinkToChat = {
  path: Path,
  // This is the convID that we are sending into. So for group chats or small
  // teams, this is the conversation. For big teams, this is the selected
  // channel.
  convID: ChatTypes.ConversationIDKey,
  // populated for teams only
  channels: I.Map<ChatTypes.ConversationIDKey, string>, // id -> channelname
}
export type SendLinkToChat = I.RecordOf<_SendLinkToChat>

export type PathItemActionMenuView = 'root' | 'share' | 'confirm-save-media' | 'confirm-send-to-other-app'
export type _PathItemActionMenu = {
  view: PathItemActionMenuView,
  previousView: PathItemActionMenuView,
  downloadKey: ?string,
}
export type PathItemActionMenu = I.RecordOf<_PathItemActionMenu>

export type _DriverStatusUnknown = {
  type: 'unknown',
}
export type DriverStatusUnknown = I.RecordOf<_DriverStatusUnknown>

export type _DriverStatusDisabled = {
  type: 'disabled',
  isEnabling: boolean,
  isDismissed: boolean,
  // macOS only
  kextPermissionError: boolean,
}
export type DriverStatusDisabled = I.RecordOf<_DriverStatusDisabled>

export type _DriverStatusEnabled = {
  type: 'enabled',
  isDisabling: boolean,
  isNew: boolean,
  // windows only
  dokanOutdated: boolean,
  dokanUninstallExecPath?: ?string,
}
export type DriverStatusEnabled = I.RecordOf<_DriverStatusEnabled>

export type DriverStatus = DriverStatusUnknown | DriverStatusDisabled | DriverStatusEnabled

export type _SystemFileManagerIntegration = {
  driverStatus: DriverStatus,
  // This only controls if system-file-manager-integration-banner is shown in
  // Folders view. The banner always shows in Settings/Files screen.
  showingBanner: boolean,
}
export type SystemFileManagerIntegration = I.RecordOf<_SystemFileManagerIntegration>

export type KbfsDaemonStatus = 'unknown' | 'waiting' | 'connected' | 'wait-timeout'

export type _State = {|
  downloads: Downloads,
  edits: Edits,
  errors: I.Map<string, FsError>,
  kbfsDaemonStatus: KbfsDaemonStatus,
  loadingPaths: I.Map<Path, I.Set<string>>,
  localHTTPServerInfo: LocalHTTPServer,
  destinationPicker: DestinationPicker,
  sendLinkToChat: SendLinkToChat,
  pathItemActionMenu: PathItemActionMenu,
  pathItems: PathItems,
  pathUserSettings: I.Map<Path, PathUserSetting>,
  sendAttachmentToChat: SendAttachmentToChat,
  sendLinkToChat: SendLinkToChat,
  sfmi: SystemFileManagerIntegration,
  tlfUpdates: UserTlfUpdates,
  tlfs: Tlfs,
  uploads: Uploads,
|}
export type State = I.RecordOf<_State>

export type Visibility = TlfType | null

export const direntToPathType = (d: RPCTypes.Dirent): PathType => {
  switch (d.direntType) {
    case RPCTypes.simpleFSDirentType.dir:
      return 'folder'
    case RPCTypes.simpleFSDirentType.sym:
      return 'symlink'
    case RPCTypes.simpleFSDirentType.file:
    case RPCTypes.simpleFSDirentType.exec:
      return 'file'
    default:
      return 'unknown'
  }
}

export const stringToEditID = (s: string): EditID => s
export const editIDToString = (s: EditID): string => s
export const stringToPath = (s: string): Path => (s.indexOf('/') === 0 ? s : null)
export const pathToString = (p: Path): string => (!p ? '' : p)
export const stringToLocalPath = (s: string): LocalPath => s
export const localPathToString = (p: LocalPath): string => p
export const getPathName = (p: Path): string => (!p ? '' : p.split('/').pop())
export const getPathNameFromElems = (elems: Array<string>): string => {
  if (elems.length === 0) return ''
  return elems[elems.length - 1]
}
export const getPathLevel = (p: Path): number => (!p ? 0 : getPathElements(p).length)
export const getPathParent = (p: Path): Path =>
  !p
    ? ''
    : p
        .split('/')
        .slice(0, -1)
        .join('/')
export const getPathElements = memoize<Path, void, void, void, _>(
  (p: Path): Array<string> => (!p ? [] : p.split('/').slice(1))
)
export const getPathFromElements = (elems: Array<string>): Path => [''].concat(elems).join('/')
export const getVisibilityFromElems = (elems: Array<string>) => {
  if (elems.length < 2 || !elems[1]) return null
  const visibility = elems[1]
  switch (visibility) {
    case 'private':
    case 'public':
    case 'team':
      return visibility
    default:
      // We don't do a flow check here because by now flow knows that we can't
      // be an empty string, so asserting empty will always fail.
      return null
  }
}
export const pathIsInTlfPath = (path: Path, tlfPath: Path) => {
  const strPath = pathToString(path)
  const strTlfPath = pathToString(tlfPath)
  return (
    strPath.startsWith(strTlfPath) &&
    (strPath.length === strTlfPath.length || strPath[strTlfPath.length] === '/')
  )
}
export const getRPCFolderTypeFromVisibility = (v: Visibility): RPCTypes.FolderType => {
  if (v === null) return RPCTypes.favoriteFolderType.unknown
  return RPCTypes.favoriteFolderType[v]
}
export const getVisibilityFromRPCFolderType = (folderType: RPCTypes.FolderType): Visibility => {
  switch (folderType) {
    case RPCTypes.favoriteFolderType.private:
      return 'private'
    case RPCTypes.favoriteFolderType.public:
      return 'public'
    case RPCTypes.favoriteFolderType.team:
      return 'team'
    default:
      return null
  }
}
export const getPathVisibility = (p: Path): Visibility => {
  const elems = getPathElements(p)
  return getVisibilityFromElems(elems)
}
export const stringToPathType = (s: string): PathType => {
  switch (s) {
    case 'folder':
    case 'file':
    case 'symlink':
    case 'unknown':
      return s
    default:
      // We don't do a flow check here because by now flow knows that we can't
      // be an empty string, so asserting empty will always fail.
      throw new Error('Invalid path type')
  }
}
export const pathTypeToString = (p: PathType): string => p
export const pathConcat = (p: Path, s: string): Path =>
  s === '' ? p : p === '/' ? stringToPath('/' + s) : stringToPath(pathToString(p) + '/' + s)
export const pathIsNonTeamTLFList = (p: Path): boolean => {
  const str = pathToString(p)
  return str === '/keybase/private' || str === '/keybase/public'
}
export const getPathDir = (p: Path): Path => pathToString(p).slice(0, pathToString(p).lastIndexOf('/'))

const localSep = isWindows ? '\\' : '/'

export const localPathConcat = (p: LocalPath, s: string): LocalPath => p + localSep + s
export const getLocalPathName = (localPath: LocalPath): string => {
  const elems = localPath.split(localSep)
  for (let elem = elems.pop(); elems.length; elem = elems.pop()) {
    if (elem !== '') {
      return elem
    }
  }
  return ''
}
export const getLocalPathDir = (p: LocalPath): string => p.slice(0, p.lastIndexOf(localSep))
export const getNormalizedLocalPath = (p: LocalPath): LocalPath =>
  localSep === '\\' ? p.replace(/\\/g, '/') : p

type sortSettingDisplayParams = {
  sortSettingText: string,
  sortSettingIconType: IconType,
}

export const sortSettingToIconTypeAndText = (s: SortSetting): sortSettingDisplayParams => {
  switch (s.sortBy) {
    case 'name':
      return s.sortOrder === 'asc'
        ? {
            sortSettingIconType: 'iconfont-arrow-full-down',
            sortSettingText: 'Name ascending',
          }
        : {
            sortSettingIconType: 'iconfont-arrow-full-up',
            sortSettingText: 'Name descending',
          }
    case 'time':
      return s.sortOrder === 'asc'
        ? {
            sortSettingIconType: 'iconfont-time',
            sortSettingText: 'Recent first',
          }
        : {
            sortSettingIconType: 'iconfont-time-reversed',
            sortSettingText: 'Older first',
          }
    default:
      throw new Error('invalid SortBy')
  }
}

export type PathItemIconSpec =
  | {
      type: 'teamAvatar',
      teamName: string,
    }
  | {
      type: 'avatar',
      username: string,
    }
  | {
      type: 'avatars',
      usernames: Array<string>,
    }
  | {
      type: 'basic',
      iconType: IconType,
      iconColor: string,
    }

export type ItemStyles = {
  iconSpec: PathItemIconSpec,
  textColor: string,
  textType: TextType,
}

export type PathBreadcrumbItem = {
  isTeamTlf: boolean,
  isLastItem: boolean,
  name: string,
  path: Path,
  onClick: (evt?: SyntheticEvent<>) => void,
}

export type FolderRPCWithMeta = {
  name: string,
  folderType: RPCTypes.FolderType,
  isIgnored: boolean,
  isNew: boolean,
  needsRekey: boolean,
  waitingForParticipantUnlock?: Array<ParticipantUnlock>,
  youCanUnlock?: Array<Device>,
  team_id: ?string,
  reset_members: ?Array<ResetMember>,
}

export type FavoriteFolder = {
  name: string,
  private: boolean,
  folderType: RPCTypes.FolderType,
  problem_set?: {
    // Map of UID to a list of KIDs, for this folder
    solution_kids: {[string]: Array<string>},
    can_self_help: boolean,
  },
  team_id: ?string,
  reset_members: ?Array<ResetMember>,
}

export type FileViewType = 'text' | 'image' | 'av' | 'pdf' | 'default'

export type ResetMetadata = {
  badgeIDKey: TeamsTypes.ResetUserBadgeIDKey,
  name: string,
  visibility: Visibility,
  resetParticipants: Array<string>,
}

// RefreshTag is used by components in FsGen.folderListLoad and
// FsGen.mimeTypeLoad actions, to indicate that it's interested in refreshing
// such data if some FS activity notification indicates it may have changed.
// Note that this is not a subscrition based model where a component needs to
// unsubscribe when it's not interested anymore. Instead, we use a simple
// heuristic where Saga only keeps track of latest call from each component and
// refresh only the most recently reuested paths for each component.
export type RefreshTag = 'main' | 'path-item-action-popup' | 'destination-picker'

export type PathItemBadge = 'upload' | 'download' | 'new' | 'rekey' | number

export type ResetBannerType = 'none' | 'self' | number
