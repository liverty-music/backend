// SPDX-License-Identifier: MIT
pragma solidity ^0.8.24;

import {Script, console} from "forge-std/Script.sol";
import {TicketSBT} from "../src/TicketSBT.sol";

/// @notice Deploy TicketSBT to Base Sepolia (or any EVM chain).
///
/// Usage:
///   forge script script/Deploy.s.sol \
///     --rpc-url <BASE_SEPOLIA_RPC_URL> \
///     --private-key <DEPLOYER_PRIVATE_KEY> \
///     --broadcast
contract DeployTicketSBT is Script {
    function run() external {
        // When using --private-key flag, msg.sender is the deployer.
        // Optionally override with DEPLOYER_ADDRESS env var.
        address deployer = vm.envOr("DEPLOYER_ADDRESS", msg.sender);

        vm.startBroadcast();

        TicketSBT sbt = new TicketSBT(deployer);

        vm.stopBroadcast();

        console.log("TicketSBT deployed at:", address(sbt));
        console.log("Admin / MINTER_ROLE:  ", deployer);
    }
}
