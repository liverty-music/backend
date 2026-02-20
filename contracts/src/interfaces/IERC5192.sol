// SPDX-License-Identifier: CC0-1.0
pragma solidity ^0.8.24;

/// @title IERC5192 — Minimal Soulbound NFT interface
/// @dev https://eips.ethereum.org/EIPS/eip-5192
interface IERC5192 {
    /// @notice Emitted when the locking status is changed to locked.
    /// @dev If a token is minted and the status is locked, this event should be emitted.
    event Locked(uint256 tokenId);

    /// @notice Emitted when the locking status is changed to unlocked.
    event Unlocked(uint256 tokenId);

    /// @notice Returns the locking status of an EIP-5192 token.
    /// @dev Reverts if the token does not exist.
    function locked(uint256 tokenId) external view returns (bool);
}
