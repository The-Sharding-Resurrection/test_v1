// SPDX-License-Identifier: MIT
pragma solidity ^0.8.23;

contract RestaurantBooking {
    uint256 public constant MAX_TABLES = 300;
    uint256 public bookedCount;
    address[MAX_TABLES] public bookings;

    function checkAvailability() public view {
        require(bookedCount < MAX_TABLES, "No more tables available");
    }

    function book(address account) public {
        bookings[bookedCount++] = account;
    }

    function getBookedCount() public view returns (uint256) {
        return bookedCount;
    }
}
