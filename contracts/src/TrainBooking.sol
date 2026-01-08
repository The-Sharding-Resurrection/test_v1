// SPDX-License-Identifier: MIT
pragma solidity ^0.8.23;

contract TrainBooking {
  uint256 public constant MAX_SEATS = 300;
  uint256 public ticketSold;
  address[MAX_SEATS] public tickets;

  function checkSeatAvailability() public view {
    require(ticketSold < MAX_SEATS, "No more ticket available");
  }

  function bookTrain(address account) public {
    tickets[ticketSold++] = account;
  }
}
