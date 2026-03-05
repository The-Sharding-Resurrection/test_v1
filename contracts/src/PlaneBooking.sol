// SPDX-License-Identifier: MIT
pragma solidity ^0.8.23;

contract PlaneBooking {
    uint256 public constant MAX_SEATS = 300;
    uint256 public bookedCount;
    address[MAX_SEATS] public bookings;

    function checkAvailability() public view {
        require(bookedCount < MAX_SEATS, "No more seats available");
    }

    function book(address account) public {
        bookings[bookedCount++] = account;
    }

    function getBookedCount() public view returns (uint256) {
        return bookedCount;
    }
}
