// @flow
import * as Types from '../../constants/types/chat2'
import * as Chat2Gen from '../../actions/chat2-gen'
import DeleteHistoryWarning from '.'
import {type RouteProps} from '../../route-tree/render-route'
import {compose, connect, isMobile} from '../../util/container'

type OwnProps = RouteProps<{conversationIDKey: Types.ConversationIDKey}, {}>

const mapStateToProps = (state, {routeProps}: OwnProps) => ({})

const mapDispatchToProps = (dispatch, {navigateUp, routeProps}: OwnProps) => ({
  onBack: isMobile ? null : () => dispatch(navigateUp()),
  onCancel: () => dispatch(navigateUp()),
  onDeleteHistory: () => {
    const conversationIDKey = routeProps.get('conversationIDKey')
    dispatch(navigateUp())
    dispatch(Chat2Gen.createMessageDeleteHistory({conversationIDKey}))
  },
})

const mergeProps = (stateProps, dispatchProps) => ({
  onBack: dispatchProps.onBack,
  onCancel: dispatchProps.onCancel,
  onDeleteHistory: dispatchProps.onDeleteHistory,
})

export default compose(
  connect<OwnProps, _, _, _, _>(
    mapStateToProps,
    mapDispatchToProps,
    mergeProps
  )
)(DeleteHistoryWarning)
