# Bloem plugin broker extensions

`pluginhost.Config.BrokerRegistrar` optionally registers additional gRPC services
on the existing host callback broker. RuntimeHost remains registered on that
same server. With no registrar and no RuntimeHost services configured, the host
keeps its existing behavior and does not bind a callback stream.

The registrar receives the plugin ID from the installed manifest and the
installation ID supplied by the host. Services must bind their caller scope to
these values, rather than accepting identity from request fields. Registration
is not authorization: each request must independently validate the current
installation, grant, tenant and source authority. A registrar must not expose
privileged services to unrelated plugins or allow temporary installation IDs to
acquire persistent authority.

Registration runs once for each callback server, before it begins serving.
Callbacks may run concurrently across installations and must only register
services; database work and network calls belong in request handlers. Existing
plugins that return Unimplemented for BindHostBroker retain the existing soft
skip behavior.

The host hook is a narrow generic extension. Bloem-specific protocols and
implementation belong in separate packages. No public SDK version change is
required to register an independent service namespace.

The subprocess regression test covers unchanged ordinary plugin startup,
extension-only binding with host-supplied identity, and an extension sharing a
connection with RuntimeHost.
