// SPDX-License-Identifier: MIT
pragma solidity ^0.8.24;

import {ERC721} from "@openzeppelin/contracts/token/ERC721/ERC721.sol";
import {AccessControl} from "@openzeppelin/contracts/access/AccessControl.sol";
import {IERC5192} from "./interfaces/IERC5192.sol";

/// @title TicketSBT
/// @notice ERC-721 Soulbound Token for Liverty Music event tickets.
///         Each token is permanently locked (non-transferable) per ERC-5192.
///
///         Access control:
///         - DEFAULT_ADMIN_ROLE: can grant/revoke roles. Held by the deployer EOA.
///         - MINTER_ROLE: can call mint(). Held by the backend service EOA, which
///           signs and submits mint transactions when a user purchases a ticket.
///           In production, the private key is stored in GCP Secret Manager.
///           In dev, the deployer EOA doubles as the minter.
contract TicketSBT is ERC721, AccessControl, IERC5192 {
    bytes32 public constant MINTER_ROLE = keccak256("MINTER_ROLE");

    constructor(address admin) ERC721("Liverty Music Ticket", "LMTKT") {
        _grantRole(DEFAULT_ADMIN_ROLE, admin);
        _grantRole(MINTER_ROLE, admin);
    }

    /// @notice Mint a ticket SBT to a recipient.
    /// @param recipient The Smart Account address that will receive the token.
    /// @param tokenId   Unique token ID (assigned by the backend, matches tickets.token_id).
    function mint(address recipient, uint256 tokenId) external onlyRole(MINTER_ROLE) {
        _mint(recipient, tokenId);
        emit Locked(tokenId);
    }

    // -------------------------------------------------------------------------
    // ERC-5192: Soulbound — all transfers are permanently locked
    // -------------------------------------------------------------------------

    /// @inheritdoc IERC5192
    function locked(uint256 tokenId) external view override returns (bool) {
        _requireOwned(tokenId);
        return true;
    }

    // -------------------------------------------------------------------------
    // ERC-721 transfer overrides — revert unconditionally
    // -------------------------------------------------------------------------

    function transferFrom(address, address, uint256) public pure override {
        revert("SBT: Ticket transfer is prohibited");
    }

    function safeTransferFrom(address, address, uint256, bytes memory) public pure override {
        revert("SBT: Ticket transfer is prohibited");
    }

    // -------------------------------------------------------------------------
    // ERC-165 supportsInterface
    // -------------------------------------------------------------------------

    function supportsInterface(bytes4 interfaceId)
        public
        view
        override(ERC721, AccessControl)
        returns (bool)
    {
        return interfaceId == type(IERC5192).interfaceId || super.supportsInterface(interfaceId);
    }
}
