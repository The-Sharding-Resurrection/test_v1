// SPDX-License-Identifier: MIT
pragma solidity ^0.8.23;

contract TravelAgency {
    // Required booking contracts (must always succeed)
    address public immutable trainBooking;
    address public immutable hotelBooking;
    
    // Optional booking contracts (only checked if address != 0)
    address public immutable planeBooking;
    address public immutable taxiBooking;
    address public immutable yachtBooking;
    address public immutable movieBooking;
    address public immutable restaurantBooking;
    
    mapping(address => bool) customers;

    constructor(
        address _trainBooking,
        address _hotelBooking,
        address _planeBooking,
        address _taxiBooking,
        address _yachtBooking,
        address _movieBooking,
        address _restaurantBooking
    ) {
        trainBooking = _trainBooking;
        hotelBooking = _hotelBooking;
        planeBooking = _planeBooking;
        taxiBooking = _taxiBooking;
        yachtBooking = _yachtBooking;
        movieBooking = _movieBooking;
        restaurantBooking = _restaurantBooking;
    }

    // Legacy function for backward compatibility (only train + hotel)
    function bookTrainAndHotel() public {
        _bookRequired();
        customers[msg.sender] = true;
    }

    // Full booking with optional services
    // Set flags to true for services you want to book
    function bookTrip(
        bool bookPlane,
        bool bookTaxi,
        bool bookYacht,
        bool bookMovie,
        bool bookRestaurant
    ) public {
        // Required bookings - must succeed
        _bookRequired();
        
        // Optional bookings - only check if requested AND contract exists
        if (bookPlane && planeBooking != address(0)) {
            _bookPlane();
        }
        if (bookTaxi && taxiBooking != address(0)) {
            _bookTaxi();
        }
        if (bookYacht && yachtBooking != address(0)) {
            _bookYacht();
        }
        if (bookMovie && movieBooking != address(0)) {
            _bookMovie();
        }
        if (bookRestaurant && restaurantBooking != address(0)) {
            _bookRestaurant();
        }
        
        customers[msg.sender] = true;
    }

    // Read-only function for checking availability (for read transactions)
    function checkAvailability(
        bool checkPlane,
        bool checkTaxi,
        bool checkYacht,
        bool checkMovie,
        bool checkRestaurant
    ) public view returns (bool) {
        bool available;
        
        // Check required services
        (available, ) = trainBooking.staticcall(abi.encodeWithSignature("checkSeatAvailability()"));
        if (!available) return false;
        
        (available, ) = hotelBooking.staticcall(abi.encodeWithSignature("checkRoomAvailability()"));
        if (!available) return false;
        
        // Check optional services
        if (checkPlane && planeBooking != address(0)) {
            (available, ) = planeBooking.staticcall(abi.encodeWithSignature("checkAvailability()"));
            if (!available) return false;
        }
        if (checkTaxi && taxiBooking != address(0)) {
            (available, ) = taxiBooking.staticcall(abi.encodeWithSignature("checkAvailability()"));
            if (!available) return false;
        }
        if (checkYacht && yachtBooking != address(0)) {
            (available, ) = yachtBooking.staticcall(abi.encodeWithSignature("checkAvailability()"));
            if (!available) return false;
        }
        if (checkMovie && movieBooking != address(0)) {
            (available, ) = movieBooking.staticcall(abi.encodeWithSignature("checkAvailability()"));
            if (!available) return false;
        }
        if (checkRestaurant && restaurantBooking != address(0)) {
            (available, ) = restaurantBooking.staticcall(abi.encodeWithSignature("checkAvailability()"));
            if (!available) return false;
        }
        
        return true;
    }

    // Internal function for required bookings
    function _bookRequired() internal {
        bool success;
        
        // Check availability first
        (success, ) = trainBooking.staticcall(abi.encodeWithSignature("checkSeatAvailability()"));
        require(success, "Train seat is not available.");
        
        (success, ) = hotelBooking.staticcall(abi.encodeWithSignature("checkRoomAvailability()"));
        require(success, "Hotel room is not available.");
        
        // Book
        (success, ) = trainBooking.call(abi.encodeWithSignature("bookTrain(address)", msg.sender));
        require(success, "Train booking failed.");
        
        (success, ) = hotelBooking.call(abi.encodeWithSignature("bookHotel(address)", msg.sender));
        require(success, "Hotel booking failed.");
    }

    function _bookPlane() internal {
        bool success;
        (success, ) = planeBooking.staticcall(abi.encodeWithSignature("checkAvailability()"));
        require(success, "Plane seat is not available.");
        (success, ) = planeBooking.call(abi.encodeWithSignature("book(address)", msg.sender));
        require(success, "Plane booking failed.");
    }

    function _bookTaxi() internal {
        bool success;
        (success, ) = taxiBooking.staticcall(abi.encodeWithSignature("checkAvailability()"));
        require(success, "Taxi is not available.");
        (success, ) = taxiBooking.call(abi.encodeWithSignature("book(address)", msg.sender));
        require(success, "Taxi booking failed.");
    }

    function _bookYacht() internal {
        bool success;
        (success, ) = yachtBooking.staticcall(abi.encodeWithSignature("checkAvailability()"));
        require(success, "Yacht is not available.");
        (success, ) = yachtBooking.call(abi.encodeWithSignature("book(address)", msg.sender));
        require(success, "Yacht booking failed.");
    }

    function _bookMovie() internal {
        bool success;
        (success, ) = movieBooking.staticcall(abi.encodeWithSignature("checkAvailability()"));
        require(success, "Movie ticket is not available.");
        (success, ) = movieBooking.call(abi.encodeWithSignature("book(address)", msg.sender));
        require(success, "Movie booking failed.");
    }

    function _bookRestaurant() internal {
        bool success;
        (success, ) = restaurantBooking.staticcall(abi.encodeWithSignature("checkAvailability()"));
        require(success, "Restaurant table is not available.");
        (success, ) = restaurantBooking.call(abi.encodeWithSignature("book(address)", msg.sender));
        require(success, "Restaurant booking failed.");
    }
}