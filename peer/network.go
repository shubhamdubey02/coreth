// (c) 2019-2022, Ava Labs, Inc. All rights reserved.
// See the file LICENSE for licensing terms.

package peer

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"golang.org/x/sync/semaphore"

	"github.com/ethereum/go-ethereum/log"

	"github.com/shubhamdubey02/cryftgo/codec"
	"github.com/shubhamdubey02/cryftgo/ids"
	"github.com/shubhamdubey02/cryftgo/network/p2p"
	"github.com/shubhamdubey02/cryftgo/snow/engine/common"
	"github.com/shubhamdubey02/cryftgo/snow/validators"
	"github.com/shubhamdubey02/cryftgo/utils"
	"github.com/shubhamdubey02/cryftgo/utils/set"
	"github.com/shubhamdubey02/cryftgo/version"

	"github.com/shubhamdubey02/coreth/peer/stats"
	"github.com/shubhamdubey02/coreth/plugin/evm/message"
)

// Minimum amount of time to handle a request
const minRequestHandlingDuration = 100 * time.Millisecond

var (
	errAcquiringSemaphore                      = errors.New("error acquiring semaphore")
	errExpiredRequest                          = errors.New("expired request")
	_                     Network              = &network{}
	_                     validators.Connector = &network{}
	_                     common.AppHandler    = &network{}
)

type Network interface {
	validators.Connector
	common.AppHandler

	// SendAppRequestAny synchronously sends request to an arbitrary peer with a
	// node version greater than or equal to minVersion.
	// Returns the ID of the chosen peer, and an error if the request could not
	// be sent to a peer with the desired [minVersion].
	SendAppRequestAny(ctx context.Context, minVersion *version.Application, message []byte, handler message.ResponseHandler) (ids.NodeID, error)

	// SendAppRequest sends message to given nodeID, notifying handler when there's a response or timeout
	SendAppRequest(ctx context.Context, nodeID ids.NodeID, message []byte, handler message.ResponseHandler) error

	// Shutdown stops all peer channel listeners and marks the node to have stopped
	// n.Start() can be called again but the peers will have to be reconnected
	// by calling OnPeerConnected for each peer
	Shutdown()

	// SetGossipHandler sets the provided gossip handler as the gossip handler
	SetGossipHandler(handler message.GossipHandler)

	// SetRequestHandler sets the provided request handler as the request handler
	SetRequestHandler(handler message.RequestHandler)

	// Size returns the size of the network in number of connected peers
	Size() uint32

	// TrackBandwidth should be called for each valid request with the bandwidth
	// (length of response divided by request time), and with 0 if the response is invalid.
	TrackBandwidth(nodeID ids.NodeID, bandwidth float64)

	// NewClient returns a client to send messages with for the given protocol
	NewClient(protocol uint64, options ...p2p.ClientOption) *p2p.Client
	// AddHandler registers a server handler for an application protocol
	AddHandler(protocol uint64, handler p2p.Handler) error
}

// network is an implementation of Network that processes message requests for
// each peer in linear fashion
type network struct {
	lock                       sync.RWMutex                       // lock for mutating state of this Network struct
	self                       ids.NodeID                         // NodeID of this node
	requestIDGen               uint32                             // requestID counter used to track outbound requests
	outstandingRequestHandlers map[uint32]message.ResponseHandler // maps avalanchego requestID => message.ResponseHandler
	activeAppRequests          *semaphore.Weighted                // controls maximum number of active outbound requests
	p2pNetwork                 *p2p.Network
	appSender                  common.AppSender          // avalanchego AppSender for sending messages
	codec                      codec.Manager             // Codec used for parsing messages
	appRequestHandler          message.RequestHandler    // maps request type => handler
	gossipHandler              message.GossipHandler     // maps gossip type => handler
	peers                      *peerTracker              // tracking of peers & bandwidth
	appStats                   stats.RequestHandlerStats // Provide request handler metrics

	// Set to true when Shutdown is called, after which all operations on this
	// struct are no-ops.
	//
	// Invariant: Even though `closed` is an atomic, `lock` is required to be
	// held when sending requests to guarantee that the network isn't closed
	// during these calls. This is because closing the network cancels all
	// outstanding requests, which means we must guarantee never to register a
	// request that will never be fulfilled or cancelled.
	closed utils.Atomic[bool]
}

func NewNetwork(p2pNetwork *p2p.Network, appSender common.AppSender, codec codec.Manager, self ids.NodeID, maxActiveAppRequests int64) Network {
	return &network{
		appSender:                  appSender,
		codec:                      codec,
		self:                       self,
		outstandingRequestHandlers: make(map[uint32]message.ResponseHandler),
		activeAppRequests:          semaphore.NewWeighted(maxActiveAppRequests),
		p2pNetwork:                 p2pNetwork,
		gossipHandler:              message.NoopMempoolGossipHandler{},
		appRequestHandler:          message.NoopRequestHandler{},
		peers:                      NewPeerTracker(),
		appStats:                   stats.NewRequestHandlerStats(),
	}
}

// SendAppRequestAny synchronously sends request to an arbitrary peer with a
// node version greater than or equal to minVersion. If minVersion is nil,
// the request will be sent to any peer regardless of their version.
// Returns the ID of the chosen peer, and an error if the request could not
// be sent to a peer with the desired [minVersion].
func (n *network) SendAppRequestAny(ctx context.Context, minVersion *version.Application, request []byte, handler message.ResponseHandler) (ids.NodeID, error) {
	// If the context was cancelled, we can skip sending this request.
	if err := ctx.Err(); err != nil {
		return ids.EmptyNodeID, err
	}

	// Take a slot from total [activeAppRequests] and block until a slot becomes available.
	if err := n.activeAppRequests.Acquire(ctx, 1); err != nil {
		return ids.EmptyNodeID, errAcquiringSemaphore
	}

	n.lock.Lock()
	defer n.lock.Unlock()
	if nodeID, ok := n.peers.GetAnyPeer(minVersion); ok {
		return nodeID, n.sendAppRequest(ctx, nodeID, request, handler)
	}

	n.activeAppRequests.Release(1)
	return ids.EmptyNodeID, fmt.Errorf("no peers found matching version %s out of %d peers", minVersion, n.peers.Size())
}

// SendAppRequest sends request message bytes to specified nodeID, notifying the responseHandler on response or failure
func (n *network) SendAppRequest(ctx context.Context, nodeID ids.NodeID, request []byte, responseHandler message.ResponseHandler) error {
	if nodeID == ids.EmptyNodeID {
		return fmt.Errorf("cannot send request to empty nodeID, nodeID=%s, requestLen=%d", nodeID, len(request))
	}

	// If the context was cancelled, we can skip sending this request.
	if err := ctx.Err(); err != nil {
		return err
	}

	// Take a slot from total [activeAppRequests] and block until a slot becomes available.
	if err := n.activeAppRequests.Acquire(ctx, 1); err != nil {
		return errAcquiringSemaphore
	}

	n.lock.Lock()
	defer n.lock.Unlock()

	return n.sendAppRequest(ctx, nodeID, request, responseHandler)
}

// sendAppRequest sends request message bytes to specified nodeID and adds [responseHandler] to [outstandingRequestHandlers]
// so that it can be invoked when the network receives either a response or failure message.
// Assumes [nodeID] is never [self] since we guarantee [self] will not be added to the [peers] map.
// Releases active requests semaphore if there was an error in sending the request
// Returns an error if [appSender] is unable to make the request.
// Assumes write lock is held
func (n *network) sendAppRequest(ctx context.Context, nodeID ids.NodeID, request []byte, responseHandler message.ResponseHandler) error {
	if n.closed.Get() {
		n.activeAppRequests.Release(1)
		return nil
	}

	// If the context was cancelled, we can skip sending this request.
	if err := ctx.Err(); err != nil {
		n.activeAppRequests.Release(1)
		return err
	}

	log.Debug("sending request to peer", "nodeID", nodeID, "requestLen", len(request))
	n.peers.TrackPeer(nodeID)

	requestID := n.nextRequestID()
	n.outstandingRequestHandlers[requestID] = responseHandler

	nodeIDs := set.NewSet[ids.NodeID](1)
	nodeIDs.Add(nodeID)

	// Send app request to [nodeID].
	// On failure, release the slot from [activeAppRequests] and delete request
	// from [outstandingRequestHandlers]
	//
	// Cancellation is removed from this context to avoid erroring unexpectedly.
	// SendAppRequest should be non-blocking and any error other than context
	// cancellation is unexpected.
	//
	// This guarantees that the network should never receive an unexpected
	// AppResponse.
	ctxWithoutCancel := context.WithoutCancel(ctx)
	if err := n.appSender.SendAppRequest(ctxWithoutCancel, nodeIDs, requestID, request); err != nil {
		log.Error(
			"request to peer failed",
			"nodeID", nodeID,
			"requestID", requestID,
			"requestLen", len(request),
			"error", err,
		)

		n.activeAppRequests.Release(1)
		delete(n.outstandingRequestHandlers, requestID)
		return err
	}

	log.Debug("sent request message to peer", "nodeID", nodeID, "requestID", requestID)
	return nil
}

// AppRequest is called by avalanchego -> VM when there is an incoming AppRequest from a peer
// error returned by this function is expected to be treated as fatal by the engine
// returns error if the requestHandler returns an error
// sends a response back to the sender if length of response returned by the handler is >0
// expects the deadline to not have been passed
func (n *network) AppRequest(ctx context.Context, nodeID ids.NodeID, requestID uint32, deadline time.Time, request []byte) error {
	if n.closed.Get() {
		return nil
	}

	log.Debug("received AppRequest from node", "nodeID", nodeID, "requestID", requestID, "requestLen", len(request))

	var req message.Request
	if _, err := n.codec.Unmarshal(request, &req); err != nil {
		log.Debug("forwarding AppRequest to SDK network", "nodeID", nodeID, "requestID", requestID, "requestLen", len(request), "err", err)
		return n.p2pNetwork.AppRequest(ctx, nodeID, requestID, deadline, request)
	}

	bufferedDeadline, err := calculateTimeUntilDeadline(deadline, n.appStats)
	if err != nil {
		log.Debug("deadline to process AppRequest has expired, skipping", "nodeID", nodeID, "requestID", requestID, "err", err)
		return nil
	}

	log.Debug("processing incoming request", "nodeID", nodeID, "requestID", requestID, "req", req)
	// We make a new context here because we don't want to cancel the context
	// passed into n.AppSender.SendAppResponse below
	handleCtx, cancel := context.WithDeadline(context.Background(), bufferedDeadline)
	defer cancel()

	responseBytes, err := req.Handle(handleCtx, nodeID, requestID, n.appRequestHandler)
	switch {
	case err != nil && err != context.DeadlineExceeded:
		return err // Return a fatal error
	case responseBytes != nil:
		return n.appSender.SendAppResponse(ctx, nodeID, requestID, responseBytes) // Propagate fatal error
	default:
		return nil
	}
}

// AppResponse is invoked when there is a response received from a peer regarding a request
// Error returned by this function is expected to be treated as fatal by the engine
// If [requestID] is not known, this function will emit a log and return a nil error.
// If the response handler returns an error it is propagated as a fatal error.
func (n *network) AppResponse(ctx context.Context, nodeID ids.NodeID, requestID uint32, response []byte) error {
	log.Debug("received AppResponse from peer", "nodeID", nodeID, "requestID", requestID)

	handler, exists := n.markRequestFulfilled(requestID)
	if !exists {
		log.Debug("forwarding AppResponse to SDK network", "nodeID", nodeID, "requestID", requestID, "responseLen", len(response))
		return n.p2pNetwork.AppResponse(ctx, nodeID, requestID, response)
	}

	// We must release the slot
	n.activeAppRequests.Release(1)

	return handler.OnResponse(response)
}

// AppRequestFailed can be called by the avalanchego -> VM in following cases:
// - node is benched
// - failed to send message to [nodeID] due to a network issue
// - request times out before a response is provided
// error returned by this function is expected to be treated as fatal by the engine
// returns error only when the response handler returns an error
func (n *network) AppRequestFailed(ctx context.Context, nodeID ids.NodeID, requestID uint32, appErr *common.AppError) error {
	log.Debug("received AppRequestFailed from peer", "nodeID", nodeID, "requestID", requestID)

	handler, exists := n.markRequestFulfilled(requestID)
	if !exists {
		log.Debug("forwarding AppRequestFailed to SDK network", "nodeID", nodeID, "requestID", requestID)
		return n.p2pNetwork.AppRequestFailed(ctx, nodeID, requestID, appErr)
	}

	// We must release the slot
	n.activeAppRequests.Release(1)

	return handler.OnFailure()
}

// calculateTimeUntilDeadline calculates the time until deadline and drops it if we missed he deadline to response.
// This function updates metrics for app requests.
// This is called by [AppRequest].
func calculateTimeUntilDeadline(deadline time.Time, stats stats.RequestHandlerStats) (time.Time, error) {
	// calculate how much time is left until the deadline
	timeTillDeadline := time.Until(deadline)
	stats.UpdateTimeUntilDeadline(timeTillDeadline)

	// bufferedDeadline is half the time till actual deadline so that the message has a reasonable chance
	// of completing its processing and sending the response to the peer.
	bufferedDeadline := time.Now().Add(timeTillDeadline / 2)

	// check if we have enough time to handle this request
	if time.Until(bufferedDeadline) < minRequestHandlingDuration {
		// Drop the request if we already missed the deadline to respond.
		stats.IncDeadlineDroppedRequest()
		return time.Time{}, errExpiredRequest
	}

	return bufferedDeadline, nil
}

// markRequestFulfilled fetches the handler for [requestID] and marks the request with [requestID] as having been fulfilled.
// This is called by either [AppResponse] or [AppRequestFailed].
// Assumes that the write lock is not held.
func (n *network) markRequestFulfilled(requestID uint32) (message.ResponseHandler, bool) {
	n.lock.Lock()
	defer n.lock.Unlock()

	handler, exists := n.outstandingRequestHandlers[requestID]
	if !exists {
		return nil, false
	}
	// mark message as processed
	delete(n.outstandingRequestHandlers, requestID)

	return handler, true
}

// AppGossip is called by avalanchego -> VM when there is an incoming AppGossip
// from a peer. An error returned by this function is treated as fatal by the
// engine.
func (n *network) AppGossip(ctx context.Context, nodeID ids.NodeID, gossipBytes []byte) error {
	var gossipMsg message.GossipMessage
	if _, err := n.codec.Unmarshal(gossipBytes, &gossipMsg); err != nil {
		log.Debug("forwarding AppGossip to SDK network", "nodeID", nodeID, "gossipLen", len(gossipBytes), "err", err)
		return n.p2pNetwork.AppGossip(ctx, nodeID, gossipBytes)
	}

	log.Debug("processing AppGossip from node", "nodeID", nodeID, "msg", gossipMsg)
	return gossipMsg.Handle(n.gossipHandler, nodeID)
}

// Connected adds the given nodeID to the peer list so that it can receive messages
func (n *network) Connected(ctx context.Context, nodeID ids.NodeID, nodeVersion *version.Application) error {
	log.Debug("adding new peer", "nodeID", nodeID)

	n.lock.Lock()
	defer n.lock.Unlock()

	if n.closed.Get() {
		return nil
	}

	if nodeID != n.self {
		// The legacy peer tracker doesn't expect to be connected to itself.
		n.peers.Connected(nodeID, nodeVersion)
	}

	return n.p2pNetwork.Connected(ctx, nodeID, nodeVersion)
}

// Disconnected removes given [nodeID] from the peer list
func (n *network) Disconnected(ctx context.Context, nodeID ids.NodeID) error {
	log.Debug("disconnecting peer", "nodeID", nodeID)
	n.lock.Lock()
	defer n.lock.Unlock()

	if n.closed.Get() {
		return nil
	}

	if nodeID != n.self {
		// The legacy peer tracker doesn't expect to be connected to itself.
		n.peers.Disconnected(nodeID)
	}

	return n.p2pNetwork.Disconnected(ctx, nodeID)
}

// Shutdown disconnects all peers
func (n *network) Shutdown() {
	n.lock.Lock()
	defer n.lock.Unlock()

	// clean up any pending requests
	for requestID, handler := range n.outstandingRequestHandlers {
		_ = handler.OnFailure() // make sure all waiting threads are unblocked
		delete(n.outstandingRequestHandlers, requestID)
	}

	n.peers = NewPeerTracker() // reset peers
	n.closed.Set(true)         // mark network as closed
}

func (n *network) SetGossipHandler(handler message.GossipHandler) {
	n.lock.Lock()
	defer n.lock.Unlock()

	n.gossipHandler = handler
}

func (n *network) SetRequestHandler(handler message.RequestHandler) {
	n.lock.Lock()
	defer n.lock.Unlock()

	n.appRequestHandler = handler
}

func (n *network) Size() uint32 {
	n.lock.RLock()
	defer n.lock.RUnlock()

	return uint32(n.peers.Size())
}

func (n *network) TrackBandwidth(nodeID ids.NodeID, bandwidth float64) {
	n.lock.Lock()
	defer n.lock.Unlock()

	n.peers.TrackBandwidth(nodeID, bandwidth)
}

func (n *network) NewClient(protocol uint64, options ...p2p.ClientOption) *p2p.Client {
	return n.p2pNetwork.NewClient(protocol, options...)
}

func (n *network) AddHandler(protocol uint64, handler p2p.Handler) error {
	return n.p2pNetwork.AddHandler(protocol, handler)
}

// invariant: peer/network must use explicitly even request ids.
// for this reason, [n.requestID] is initialized as zero and incremented by 2.
// This is for backwards-compatibility while the SDK router exists with the
// legacy coreth handlers to avoid a (very) narrow edge case where request ids
// can overlap, resulting in a dropped timeout.
func (n *network) nextRequestID() uint32 {
	next := n.requestIDGen
	n.requestIDGen += 2

	return next
}