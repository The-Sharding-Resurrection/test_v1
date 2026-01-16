// SPDX-License-Identifier: MIT
pragma solidity ^0.8.23;

contract MovieBooking {
    uint256 public constant MAX_TICKETS = 300;
    uint256 public bookedCount;
    address[MAX_TICKETS] public bookings;

    function checkAvailability() public view {
        require(bookedCount < MAX_TICKETS, "No more tickets available");
    }

    function book(address account) public {
        bookings[bookedCount++] = account;
    }

    function getBookedCount() public view returns (uint256) {
        return bookedCount;
    }
}
