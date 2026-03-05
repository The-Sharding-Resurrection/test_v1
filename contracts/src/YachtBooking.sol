// SPDX-License-Identifier: MIT
pragma solidity ^0.8.23;

contract YachtBooking {
    uint256 public constant MAX_YACHTS = 300;
    uint256 public bookedCount;
    address[MAX_YACHTS] public bookings;

    function checkAvailability() public view {
        require(bookedCount < MAX_YACHTS, "No more yachts available");
    }

    function book(address account) public {
        bookings[bookedCount++] = account;
    }

    function getBookedCount() public view returns (uint256) {
        return bookedCount;
    }
}
