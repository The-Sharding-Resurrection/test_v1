// SPDX-License-Identifier: MIT
pragma solidity ^0.8.19;

/// @notice Registry deployed on Shard 0 that coordinates state across all shards
/// @dev State is sharded by account address modulo NUM_SHARDS
contract MultiShardRegistry {
    uint256 public constant NUM_SHARDS = 6;

    // Local state on this shard (shard 0)
    mapping(address => uint256) public localBalances;

    // Track which shard owns each account's state
    mapping(address => uint256) public shardAssignment;

    event BalanceUpdate(address indexed account, uint256 shard, uint256 amount);
    event CrossShardOperation(address indexed from, address indexed to, uint256 amount);

    constructor() {
        // Shard 0 owns this contract
    }

    /// @notice Determine which shard owns this account's state
    function getShardForAccount(address account) public pure returns (uint256) {
        return uint256(uint160(account)) % NUM_SHARDS;
    }

    /// @notice Update balance (only works for accounts owned by shard 0)
    function updateBalance(address account, uint256 amount) external {
        require(getShardForAccount(account) == 0, "Account state not on this shard");
        localBalances[account] = amount;
        emit BalanceUpdate(account, 0, amount);
    }

    /// @notice Simulate cross-shard transfer
    /// @dev In real implementation, this would trigger cross-shard message passing
    function transferCrossShard(
        address from,
        address to,
        uint256 amount
    ) external returns (uint256 fromShard, uint256 toShard) {
        fromShard = getShardForAccount(from);
        toShard = getShardForAccount(to);

        // If both on shard 0, do local transfer
        if (fromShard == 0 && toShard == 0) {
            require(localBalances[from] >= amount, "Insufficient balance");
            localBalances[from] -= amount;
            localBalances[to] += amount;
        }

        emit CrossShardOperation(from, to, amount);
        return (fromShard, toShard);
    }

    /// @notice Get shard assignment for multiple accounts
    function batchGetShards(address[] calldata accounts)
        external
        pure
        returns (uint256[] memory)
    {
        uint256[] memory shards = new uint256[](accounts.length);
        for (uint256 i = 0; i < accounts.length; i++) {
            shards[i] = getShardForAccount(accounts[i]);
        }
        return shards;
    }
}
