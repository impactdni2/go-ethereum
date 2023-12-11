@0xae3aed875bda8d17;

#using Go = import "/go.capnp";
#$Go.package("rpc");
#$Go.import("rpc");

using Rust = import "rust.capnp";
$Rust.parentModule("server");

struct GetStorageAtReq {
  block @0 :UInt64;
  address @1 :Data;
  slot @2 :Data;
}

#struct RpcCall {
#  method :union {
#    unknown @0: Void;
#    getStorageAt @1 :GetStorageAtParams;
#  }

#  struct GetStorageAtParams {
#    block @0 :UInt64;
#    address @1 :Data;
#    slot @2 :Data;
#  }
#}
