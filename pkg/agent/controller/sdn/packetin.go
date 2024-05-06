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
	"errors"
	"fmt"
	"net"

	"antrea.io/libOpenflow/openflow15"
	"antrea.io/libOpenflow/protocol"
	"antrea.io/libOpenflow/util"
	"antrea.io/ofnet/ofctrl"
	//"k8s.io/client-go/util/retry"
	"k8s.io/klog/v2"
	//"k8s.io/utils/ptr"

	"antrea.io/antrea/pkg/agent/openflow"
	binding "antrea.io/antrea/pkg/ovs/openflow"
)

var skipSDNUpdateErr = errors.New("skip SDN update")

func (c *SDNController) HandlePacketIn(pktIn *ofctrl.PacketIn) error {
	//if !c.sdnListerSynced() {
	//	return errors.New("SDN controller is not started")
	//}
	//_, _, packet, err := c.parsePacketIn(pktIn)

	return nil
}

func (c *SDNController) parsePacketIn(pktIn *ofctrl.PacketIn) error {
	matchers := pktIn.GetMatches()

	// Get data plane tag.
	// Directly read data plane tag from packet.
	var err error
	var tag uint8
	var ctNwDst, ctNwSrc, ipDst, ipSrc string
	etherData := new(protocol.Ethernet)
	if err := etherData.UnmarshalBinary(pktIn.Data.(*util.Buffer).Bytes()); err != nil {
		return fmt.Errorf("failed to parse Ethernet packet from packet-in message: %v", err)
	}
	if etherData.Ethertype == protocol.IPv4_MSG {
		ipPacket, ok := etherData.Data.(*protocol.IPv4)
		if !ok {
			return errors.New("invalid sdn IPv4 packet")
		}
		tag = ipPacket.DSCP
		ctNwDst, err = getCTDstValue(matchers, false)
		if err != nil {
			return err
		}
		ctNwSrc, err = getCTSrcValue(matchers, false)
		if err != nil {
			return err
		}
		ipDst = ipPacket.NWDst.String()
		ipSrc = ipPacket.NWSrc.String()
	} else if etherData.Ethertype == protocol.IPv6_MSG {
		ipv6Packet, ok := etherData.Data.(*protocol.IPv6)
		if !ok {
			return errors.New("invalid sdn IPv6 packet")
		}
		tag = ipv6Packet.TrafficClass >> 2
		ctNwDst, err = getCTDstValue(matchers, true)
		if err != nil {
			return err
		}
		ctNwSrc, err = getCTSrcValue(matchers, true)
		if err != nil {
			return err
		}
		ipDst = ipv6Packet.NWDst.String()
		ipSrc = ipv6Packet.NWSrc.String()
	} else {
		return fmt.Errorf("unsupported sdn packet Ethertype: %d", etherData.Ethertype)
	}

	firstPacket := false
	c.runningSDNsMutex.RLock()
	tfState, exists := c.runningSDNs[int8(tag)]
	if exists {
		firstPacket = !tfState.receivedPacket
		tfState.receivedPacket = true
	}
	c.runningSDNsMutex.RUnlock()
	if !exists {
		return fmt.Errorf("SDN for dataplane tag %d not found in cache", tag)
	}

	if tfState.liveTraffic {
		// Live SDN only considers the first packet of each
		// connection. However, it is possible for 2 connections to
		// match the Live SDN flows in OVS (before the flows can
		// be uninstalled below), leading to 2 Packet In messages being
		// processed. If we don't ignore all additional Packet Ins, we
		// can end up with duplicate Node observations in the SDN
		// Status. This situation is more likely when the Live SDN
		// request does not specify source / destination ports.
		if !firstPacket {
			klog.InfoS("An additional SDN packet was received unexpectedly for Live SDN, ignoring it")
			return skipSDNUpdateErr
		}
	}

	// Collect Service connections.
	// - For packet is DNATed only, the final state is that ipDst != ctNwDst (in DNAT CT zone).
	// - For packet is both DNATed and SNATed, the first state is also ipDst != ctNwDst (in DNAT CT zone), but the final
	//   state is that ipSrc != ctNwSrc (in SNAT CT zone). The state in DNAT CT zone cannot be recognized in SNAT CT zone.
	if !tfState.receiverOnly {
		if isValidCtNw(ctNwDst) && ipDst != ctNwDst || isValidCtNw(ctNwSrc) && ipSrc != ctNwSrc {
		}
		// Collect egress conjunctionID and get NetworkPolicy from cache.
		if match := getMatchRegField(matchers, openflow.TFEgressConjIDField); match != nil {
		}
	}

	// Collect ingress conjunctionID and get NetworkPolicy from cache.
	if match := getMatchRegField(matchers, openflow.TFIngressConjIDField); match != nil {
		_, err := getRegValue(match, nil)
		if err != nil {
			return err
		}
	}

	return nil
}

func getMatchPktMarkField(matchers *ofctrl.Matchers) *ofctrl.MatchField {
	return matchers.GetMatchByName("NXM_NX_PKT_MARK")
}

func getMatchRegField(matchers *ofctrl.Matchers, field *binding.RegField) *ofctrl.MatchField {
	return openflow.GetMatchFieldByRegID(matchers, field.GetRegID())
}

func getMatchTunnelDstField(matchers *ofctrl.Matchers, isIPv6 bool) *ofctrl.MatchField {
	if isIPv6 {
		return matchers.GetMatchByName("NXM_NX_TUN_IPV6_DST")
	}
	return matchers.GetMatchByName("NXM_NX_TUN_IPV4_DST")
}

func getMarkValue(match *ofctrl.MatchField) (uint32, error) {
	mark, ok := match.GetValue().(uint32)
	if !ok {
		return 0, errors.New("mark value cannot be got")
	}
	return mark, nil
}

func getRegValue(regMatch *ofctrl.MatchField, rng *openflow15.NXRange) (uint32, error) {
	regValue, ok := regMatch.GetValue().(*ofctrl.NXRegister)
	if !ok {
		return 0, errors.New("register value cannot be got")
	}
	if rng != nil {
		return ofctrl.GetUint32ValueWithRange(regValue.Data, rng), nil
	}
	return regValue.Data, nil
}

func getTunnelDstValue(regMatch *ofctrl.MatchField) (string, error) {
	regValue, ok := regMatch.GetValue().(net.IP)
	if !ok {
		return "", errors.New("tunnel destination value cannot be got")
	}
	return regValue.String(), nil
}

func getCTDstValue(matchers *ofctrl.Matchers, isIPv6 bool) (string, error) {
	var match *ofctrl.MatchField
	if isIPv6 {
		match = matchers.GetMatchByName("NXM_NX_CT_IPV6_DST")
	} else {
		match = matchers.GetMatchByName("NXM_NX_CT_NW_DST")
	}
	if match == nil {
		return "", nil
	}
	regValue, ok := match.GetValue().(net.IP)
	if !ok {
		return "", errors.New("packet-in conntrack destination value cannot be retrieved from metadata")
	}
	return regValue.String(), nil
}

func getCTSrcValue(matchers *ofctrl.Matchers, isIPv6 bool) (string, error) {
	var match *ofctrl.MatchField
	if isIPv6 {
		match = matchers.GetMatchByName("NXM_NX_CT_IPV6_SRC")
	} else {
		match = matchers.GetMatchByName("NXM_NX_CT_NW_SRC")
	}
	if match == nil {
		return "", nil
	}
	regValue, ok := match.GetValue().(net.IP)
	if !ok {
		return "", errors.New("packet-in conntrack source value cannot be retrieved from metadata")
	}
	return regValue.String(), nil
}

func isValidCtNw(ipStr string) bool {
	ip := net.ParseIP(ipStr)
	if ip == nil {
		return false
	}
	// Reserved by IETF [RFC3513][RFC4291]
	_, cidr, _ := net.ParseCIDR("0000::/8")
	if cidr.Contains(ip) {
		return false
	}
	return true
}
