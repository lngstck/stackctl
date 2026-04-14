// Package registration builds age-encrypted registration packages that a
// school admin sends to the learningstack operator for onboarding.
//
// The operator's public age key is compiled into the binary. Rotating it
// requires a new stackctl release; the operator keeps old private keys
// until all schools have self-updated.
package registration

// OperatorPublicKey is the age X25519 public key of the learningstack
// operator. Registration packages are encrypted to this recipient.
//
// The matching private key is held by the operator and NEVER distributed.
// Generate a new keypair with: age-keygen
const OperatorPublicKey = "age10c4twhrpx22cnvchaa0553mlnrq2ye7x0tpvekrrmflsvwepty3qgupnwf"
