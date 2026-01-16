// SPDX-License-Identifier: MIT
pragma solidity ^0.8.23;

contract TaxiBooking {
    uint256 public constant MAX_RIDES = 300;
    uint256 public bookedCount;
    address[MAX_RIDES] public bookings;

    function checkAvailability() public view {
        require(bookedCount < MAX_RIDES, "No more rides available");
    }

    function book(address account) public {
        bookings[bookedCount++] = account;
    }

    function getBookedCount() public view returns (uint256) {
        return bookedCount;
    }
}
