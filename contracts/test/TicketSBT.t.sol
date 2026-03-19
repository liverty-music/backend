// SPDX-License-Identifier: MIT
pragma solidity ^0.8.24;

import {Test} from "forge-std/Test.sol";
import {TicketSBT} from "../src/TicketSBT.sol";
import {IERC5192} from "../src/interfaces/IERC5192.sol";
import {IAccessControl} from "@openzeppelin/contracts/access/IAccessControl.sol";
import {IERC721} from "@openzeppelin/contracts/token/ERC721/IERC721.sol";

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

    function test_SafeTransferFromWithDataReverts() public {
        vm.prank(minter);
        sbt.mint(recipient, 1);

        vm.expectRevert("SBT: Ticket transfer is prohibited");
        vm.prank(recipient);
        sbt.safeTransferFrom(recipient, other, 1, "");
    }

    function test_SafeTransferFromReverts() public {
        vm.prank(minter);
        sbt.mint(recipient, 1);

        vm.expectRevert("SBT: Ticket transfer is prohibited");
        vm.prank(recipient);
        sbt.safeTransferFrom(recipient, other, 1);
    }

    // -------------------------------------------------------------------------
    // Approve lock
    // -------------------------------------------------------------------------

    function test_ApproveReverts() public {
        vm.prank(minter);
        sbt.mint(recipient, 1);

        vm.expectRevert("SBT: Ticket transfer is prohibited");
        vm.prank(recipient);
        sbt.approve(other, 1);
    }

    function test_SetApprovalForAllReverts() public {
        vm.expectRevert("SBT: Ticket transfer is prohibited");
        vm.prank(recipient);
        sbt.setApprovalForAll(other, true);
    }

    // -------------------------------------------------------------------------
    // Edge cases
    // -------------------------------------------------------------------------

    function test_DuplicateMintReverts() public {
        vm.prank(minter);
        sbt.mint(recipient, 1);

        vm.expectRevert();
        vm.prank(minter);
        sbt.mint(other, 1);
    }

    function test_MintToZeroAddressReverts() public {
        vm.expectRevert();
        vm.prank(minter);
        sbt.mint(address(0), 1);
    }

    // -------------------------------------------------------------------------
    // locked() reverts for non-existent token
    // -------------------------------------------------------------------------

    function test_LockedRevertsForNonExistentToken() public {
        vm.expectRevert();
        sbt.locked(9999);
    }

    // -------------------------------------------------------------------------
    // supportsInterface (ERC-165)
    // -------------------------------------------------------------------------

    function test_SupportsERC721Interface() public view {
        assertTrue(sbt.supportsInterface(type(IERC721).interfaceId));
    }

    function test_SupportsERC5192Interface() public view {
        assertTrue(sbt.supportsInterface(type(IERC5192).interfaceId));
    }

    function test_SupportsAccessControlInterface() public view {
        assertTrue(sbt.supportsInterface(type(IAccessControl).interfaceId));
    }

    function test_DoesNotSupportInvalidInterface() public view {
        assertFalse(sbt.supportsInterface(0xffffffff));
    }

    // -------------------------------------------------------------------------
    // AccessControl role management
    // -------------------------------------------------------------------------

    function test_AdminGrantsMinterRole() public {
        address newMinter = makeAddr("newMinter");
        bytes32 minterRole = sbt.MINTER_ROLE();

        vm.prank(admin);
        sbt.grantRole(minterRole, newMinter);

        assertTrue(sbt.hasRole(minterRole, newMinter));
    }

    function test_RevokedMinterCannotMint() public {
        bytes32 minterRole = sbt.MINTER_ROLE();

        vm.prank(admin);
        sbt.revokeRole(minterRole, minter);

        assertFalse(sbt.hasRole(minterRole, minter));

        vm.expectRevert();
        vm.prank(minter);
        sbt.mint(recipient, 1);
    }

    function test_NonAdminCannotGrantMinterRole() public {
        bytes32 minterRole = sbt.MINTER_ROLE();

        vm.expectRevert();
        vm.prank(other);
        sbt.grantRole(minterRole, other);
    }

    // -------------------------------------------------------------------------
    // Constructor verification
    // -------------------------------------------------------------------------

    function test_NameAndSymbol() public view {
        assertEq(sbt.name(), "Liverty Music Ticket");
        assertEq(sbt.symbol(), "LMTKT");
    }

    function test_DeployerHasAdminAndMinterRoles() public view {
        assertTrue(sbt.hasRole(sbt.DEFAULT_ADMIN_ROLE(), admin));
        assertTrue(sbt.hasRole(sbt.MINTER_ROLE(), admin));
    }

    // -------------------------------------------------------------------------
    // Fuzz tests
    // -------------------------------------------------------------------------

    function testFuzz_MintAnyTokenId(uint256 tokenId) public {
        vm.prank(minter);
        sbt.mint(recipient, tokenId);

        assertEq(sbt.ownerOf(tokenId), recipient);
    }

    function testFuzz_UnauthorizedMintReverts(address caller) public {
        vm.assume(caller != minter);
        vm.assume(caller != admin);

        vm.expectRevert();
        vm.prank(caller);
        sbt.mint(recipient, 1);
    }
}
