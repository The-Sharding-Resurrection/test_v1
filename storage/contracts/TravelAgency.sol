// SPDX-License-Identifier: MIT
pragma solidity ^0.8.23;

contract TravelAgency {
  address public immutable trainBooking;
  address public immutable hotelBooking;
  mapping(address=>bool) customers;

  constructor(address _trainBooking, address _hotelBooking) {
    trainBooking = _trainBooking;
    hotelBooking = _hotelBooking;
  }

  function bookTrainAndHotel() public {
    bool trainAvailable;
    bool hotelAvailable;
    bool bookingSuccess;

    (trainAvailable, ) = trainBooking.staticcall(abi.encodeWithSignature("checkSeatAvailability()"));
    require(trainAvailable, "Train seat is not available.");

    (hotelAvailable, ) = hotelBooking.staticcall(abi.encodeWithSignature("checkRoomAvailability()"));
    require(hotelAvailable, "Hotel room is not available.");

    (bookingSuccess, ) = trainBooking.call(abi.encodeWithSignature("bookTrain(address)",msg.sender));
    require(bookingSuccess, "Train booking failed.");

    (bookingSuccess, ) = hotelBooking.call(abi.encodeWithSignature("bookHotel(address)",msg.sender));
    require(bookingSuccess, "Hotel booking failed.");
    
    customers[msg.sender] = true;
  }
}