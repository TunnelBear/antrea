// Copyright 2020 Antrea Authors
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package sdn

import (
	//"net"
	"sync"
	"time"

	//"antrea.io/libOpenflow/protocol"
	//coreinformers "k8s.io/client-go/informers/core/v1"
	clientset "k8s.io/client-go/kubernetes"
	//corelisters "k8s.io/client-go/listers/core/v1"
	//"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/util/workqueue"
	"k8s.io/klog/v2"

	"antrea.io/antrea/pkg/agent/config"
	//"antrea.io/antrea/pkg/agent/interfacestore"
	"antrea.io/antrea/pkg/agent/openflow"
	//binding "antrea.io/antrea/pkg/ovs/openflow"
)

const (
	controllerName = "SDNController"
	// Set resyncPeriod to 0 to disable resyncing.
	resyncPeriod time.Duration = 0
	// How long to wait before retrying the processing of a sdn.
	minRetryDelay = 5 * time.Second
	maxRetryDelay = 300 * time.Second
	// Default number of workers processing sdn request.
	defaultWorkers = 4
	// Delay in milliseconds before injecting packet into OVS. The time of different nodes may not be completely
	// synchronized, which requires a delay before inject packet.
	injectPacketDelay      = 2000
	injectLocalPacketDelay = 100

	// ICMP Echo Request type and code.
	icmpEchoRequestType   uint8 = 8
	icmpv6EchoRequestType uint8 = 128
	icmpEchoRequestCode   uint8 = 0

	defaultTTL uint8 = 64
)

type sdnState struct {
	name string
	// Used to uniquely identify SDN.
	//uid         types.UID
	tag         int8
	liveTraffic bool
	droppedOnly bool
	// Live-traffic SDN with only destination Pod specified.
	receiverOnly bool
	isSender     bool
	// Agent received the first SDN packet from OVS.
	receivedPacket bool
}

// Controller is responsible for setting up Openflow entries and injecting sdn packet into
// the switch for sdn request.
type SDNController struct {
	kubeClient             clientset.Interface
	ofClient               openflow.Client
	//interfaceStore         interfacestore.InterfaceStore
	networkConfig          *config.NetworkConfig
	queue                  workqueue.RateLimitingInterface
	runningSDNsMutex sync.RWMutex
	// runningSDNs is a map for storing the running SDN state
	// with dataplane tag to be the key.
	runningSDNs map[int8]*sdnState
	enableAntreaProxy bool
}

// NewSDNController instantiates a new Controller object which will process SDN
// events.
func NewSDNController(
	kubeClient clientset.Interface,
	//serviceInformer coreinformers.ServiceInformer,
	client openflow.Client,
	//interfaceStore interfacestore.InterfaceStore,
	networkConfig *config.NetworkConfig) *SDNController {
	c := &SDNController{
		kubeClient:            kubeClient,
		ofClient:              client,
		//interfaceStore:        interfaceStore,
		networkConfig:         networkConfig,
		runningSDNs:     make(map[int8]*sdnState),
	}

	// Register packetInHandler
	c.ofClient.RegisterPacketInHandler(uint8(openflow.PacketInCategoryTF), c)
	return c
}

// Run will create defaultWorkers workers (go routines) which will process the SDN events from the
// workqueue.
func (c *SDNController) Run(stopCh <-chan struct{}) {
	defer c.queue.ShutDown()

	klog.Infof("Starting %s", controllerName)
	defer klog.Infof("Shutting down %s", controllerName)

	for i := 0; i < defaultWorkers; i++ {
		//go wait.Until(c.worker, time.Second, stopCh)
	}
	<-stopCh
}

// worker is a long-running function that will continually call the processSDNItem function
// in order to read and process a message on the workqueue.
func (c *SDNController) worker() {
	for c.processSDNItem() {
	}
}

// processSDNItem processes an item in the "sdn" work queue, by calling syncSDN
// after casting the item to a string (SDN name). If syncSDN returns an error, this
// function logs error. If syncSDN is successful, the SDN is removed from the queue
// until we get notified of a new change. This function returns false if and only if the work queue
// was shutdown (no more items will be processed).
func (c *SDNController) processSDNItem() bool {
	obj, quit := c.queue.Get()
	if quit {
		return false
	}
	// We call Done here so the workqueue knows we have finished processing this item. We also
	// must remember to call Forget if we do not want this work item being re-queued. For
	// example, we do not call Forget if a transient error occurs, instead the item is put back
	// on the workqueue and attempted again after a back-off period.
	defer c.queue.Done(obj)

	// We expect strings (SDN name) to come off the workqueue.
	if _, ok := obj.(string); !ok {
		// As the item in the workqueue is actually invalid, we call Forget here else we'd
		// go into a loop of attempting to process a work item that is invalid.
		// This should not happen: enqueueSDN only enqueues strings.
		c.queue.Forget(obj)
		klog.Errorf("Expected string in work queue but got %#v", obj)
		return true
	} else {
		// If error occurs we log error.
	}
	return true
}
