// @flow
import * as I from 'immutable'
import * as Styles from '../../styles'
import * as Types from '../../constants/types/fs'
import * as React from 'react'
import * as Kb from '../../common-adapters'
import {OpenInSystemFileManager, PathItemIcon, PathItemAction, SendInAppAction} from '../common'

export type StillCommonProps = {
  name: string,
  path: Types.Path,
  inDestinationPicker?: boolean,
  onOpen: () => void,
  routePath: I.List<string>,
}

export const StillCommon = (
  props: StillCommonProps & {
    children: React.Node,
    badge?: ?Types.PathItemBadge,
  }
) => (
  <Kb.ListItem2
    type="Small"
    icon={<PathItemIcon path={props.path} size={32} style={rowStyles.pathItemIcon} badge={props.badge} />}
    firstItem={true /* we add divider in Rows */}
    onClick={props.onOpen}
    body={props.children}
    onlyShowActionOnHover={true}
    action={
      !props.inDestinationPicker &&
      Types.getPathLevel(props.path) > 2 && (
        <Kb.Box2 direction="horizontal">
          <OpenInSystemFileManager path={props.path} />
          <SendInAppAction path={props.path} />
          <Kb.WithTooltip text="Other actions">
            <PathItemAction
              path={props.path}
              clickable={{type: 'icon'}}
              routePath={props.routePath}
              initView="root"
            />
          </Kb.WithTooltip>
        </Kb.Box2>
      )
    }
  />
)

export const rowStyles = Styles.styleSheetCreate({
  itemBox: {
    ...Styles.globalStyles.flexBoxColumn,
    flex: 1,
    justifyContent: 'center',
    minWidth: 0,
    width: 0,
  },
  pathItemIcon: {
    marginLeft: Styles.globalMargins.medium,
    marginRight: Styles.globalMargins.medium,
  },
  rowText: Styles.platformStyles({
    isElectron: {
      overflow: 'hidden',
      textOverflow: 'ellipsis',
      whiteSpace: 'nowrap',
    },
    isMobile: {
      flexShrink: 1,
    },
  }),
  rowText_30: Styles.platformStyles({
    common: {
      opacity: 0.3,
    },
    isElectron: {
      overflow: 'hidden',
      textOverflow: 'ellipsis',
      whiteSpace: 'nowrap',
    },
  }),
})

export const normalRowHeight = Kb.smallListItem2Height
