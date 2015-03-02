// Package ring contains tools for building and using a consistent hashing ring
// with replicas, automatic partitioning (ring ranges), and keeping replicas of
// the same partitions in as distinct tiered nodes as possible (tiers might be
// devices, servers, cabinets, rooms, data centers, geographical regions, etc.)
//
// It also contains tools for using a ring as a messaging hub, easing
// communication between nodes in the ring.
package ring

type Ring interface {
	// Version can indicate changes in ring data; for example, if a server is
	// currently working with one version of ring data and receives requests
	// that are based on a lesser version of ring data, it can ignore those
	// requests or send an "obsoleted" response or something along those lines.
	// Similarly, if the server receives requests for a greater version of ring
	// data, it can ignore those requests or try to obtain a newer ring
	// version.
	Version() int64
	// PartitionBitCount is the number of bits that can be used to determine a
	// partition number for the current data in the ring. For example, to
	// convert a uint64 hash value into a partition number you could use
	// hashValue >> (64 - ring.PartitionBitCount()).
	PartitionBitCount() uint16
	ReplicaCount() int
	// LocalNodeID is the identifier of the local node; determines which ring
	// partitions/replicas the local node is responsible for as well as being
	// used to direct message delivery.
	LocalNodeID() uint64
	// Responsible will return true if the local node is considered responsible
	// for a replica of the partition given.
	Responsible(partition uint32) bool
	// ResponsibleIDs will return a list of Node IDs (as with LocalNodeID) for
	// those nodes considered responsible for the replicas of the partition
	// given.
	ResponsibleIDs(partition uint32) []uint64
}

type ringImpl struct {
	version                       int64
	localNodeIndex                int32
	partitionBitCount             uint16
	nodeIDs                       []uint64
	replicaToPartitionToNodeIndex [][]int32
}

func (ring *ringImpl) Version() int64 {
	return ring.version
}

func (ring *ringImpl) PartitionBitCount() uint16 {
	return ring.partitionBitCount
}

func (ring *ringImpl) ReplicaCount() int {
	return len(ring.replicaToPartitionToNodeIndex)
}

func (ring *ringImpl) LocalNodeID() uint64 {
	if ring.localNodeIndex == 0 {
		return 0
	}
	return ring.nodeIDs[ring.localNodeIndex]
}

func (ring *ringImpl) Responsible(partition uint32) bool {
	if ring.localNodeIndex == 0 {
		return false
	}
	for _, partitionToNodeIndex := range ring.replicaToPartitionToNodeIndex {
		if partitionToNodeIndex[partition] == ring.localNodeIndex {
			return true
		}
	}
	return false
}

func (ring *ringImpl) ResponsibleIDs(partition uint32) []uint64 {
	ids := make([]uint64, ring.ReplicaCount())
	for replica, partitionToNodeIndex := range ring.replicaToPartitionToNodeIndex {
		ids[replica] = ring.nodeIDs[partitionToNodeIndex[partition]]
	}
	return ids
}
