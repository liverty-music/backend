// SPDX-License-Identifier: MIT
pragma solidity ^0.8.24;

import {Test} from "forge-std/Test.sol";
import {TicketSBT} from "../src/TicketSBT.sol";
import {IERC5192} from "../src/interfaces/IERC5192.sol";

contract TicketSBTTest is Test {
    TicketSBT internal sbt;

    address internal admin = makeAddr("admin");
    address internal minter = makeAddr("minter");
    address internal recipient = makeAddr("recipient");
    address internal other = makeAddr("other");

    function setUp() public {
        vm.startPrank(admin);
        sbt = new TicketSBT(admin);
        sbt.grantRole(sbt.MINTER_ROLE(), minter);
        vm.stopPrank();
    }

    // -------------------------------------------------------------------------
    // Authorized mint
    // -------------------------------------------------------------------------

    function test_AuthorizedMint() public {
        vm.prank(minter);
        sbt.mint(recipient, 1);

        assertEq(sbt.ownerOf(1), recipient);
    }

    function test_MintEmitsLockedEvent() public {
        vm.expectEmit(true, false, false, false, address(sbt));
        emit IERC5192.Locked(42);

        vm.prank(minter);
        sbt.mint(recipient, 42);
    }

    function test_LockedReturnsTrueForMintedToken() public {
        vm.prank(minter);
        sbt.mint(recipient, 1);

        assertTrue(sbt.locked(1));
    }

    // -------------------------------------------------------------------------
    // Unauthorized mint
    // -------------------------------------------------------------------------

    function test_UnauthorizedMintReverts() public {
        vm.expectRevert();
        vm.prank(other);
        sbt.mint(recipient, 1);
    }

    // -------------------------------------------------------------------------
    // Transfer lock
    // -------------------------------------------------------------------------

    function test_TransferFromReverts() public {
        vm.prank(minter);
        sbt.mint(recipient, 1);

        vm.expectRevert("SBT: Ticket transfer is prohibited");
        vm.prank(recipient);
        sbt.transferFrom(recipient, other, 1);
    }

    function test_SafeTransferFromReverts() public {
        vm.prank(minter);
        sbt.mint(recipient, 1);

        vm.expectRevert("SBT: Ticket transfer is prohibited");
        vm.prank(recipient);
        sbt.safeTransferFrom(recipient, other, 1, "");
    }

    // -------------------------------------------------------------------------
    // locked() reverts for non-existent token
    // -------------------------------------------------------------------------

    function test_LockedRevertsForNonExistentToken() public {
        vm.expectRevert();
        sbt.locked(9999);
    }
}
