// SPDX-License-Identifier: MIT
pragma solidity ^0.8.19;

contract ShardCounter {
    uint256 public count;
    uint256 public shardId;

    event Incremented(uint256 shardId, uint256 newCount);
    event CrossShardCall(uint256 fromShard, uint256 toShard, uint256 value);

    constructor(uint256 _shardId) {
        shardId = _shardId;
    }

    function increment() external {
        count++;
        emit Incremented(shardId, count);
    }

    function add(uint256 value) external {
        count += value;
    }

    function recordCrossShardCall(uint256 fromShard, uint256 value) external {
        count += value;
        emit CrossShardCall(fromShard, shardId, value);
    }
}
