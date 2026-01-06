// SPDX-License-Identifier: MIT
pragma solidity ^0.8.23;

contract HotelBooking {
  uint256 public constant MAX_ROOMS = 300;
  uint256 public roomReserved;
  address[MAX_ROOMS] public rooms;

  function checkRoomAvailability() public view{
    require(roomReserved < MAX_ROOMS, "No more room available");
  }

  function bookHotel(address account) public {
    rooms[roomReserved++] = account;
  }
}
