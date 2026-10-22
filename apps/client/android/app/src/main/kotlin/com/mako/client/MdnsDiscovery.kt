package com.mako.client

import android.content.Context
import android.net.nsd.NsdManager
import android.net.nsd.NsdServiceInfo
import android.os.Handler
import android.os.Looper
import io.flutter.plugin.common.EventChannel

/**
 * LAN discovery via Android's NsdManager. Emits maps:
 *   {type: "found", service, id, name, host, port, path}
 *   {type: "lost", service}
 *   {type: "error", message}
 * Discovery is only a hint; the Dart side verifies the server ID and keeps
 * certificate pinning/PIN pairing authoritative.
 */
class MdnsDiscovery(context: Context) : EventChannel.StreamHandler {
    private val appContext = context.applicationContext
    private val mainHandler = Handler(Looper.getMainLooper())
    private var sink: EventChannel.EventSink? = null
    private var nsdManager: NsdManager? = null
    private var discoveryListener: NsdManager.DiscoveryListener? = null
    private val resolving = HashSet<String>()
    private var running = false

    override fun onListen(arguments: Any?, events: EventChannel.EventSink?) {
        sink = events
        start()
    }

    override fun onCancel(arguments: Any?) {
        stop()
        sink = null
    }

    private fun start() {
        if (running) return
        val manager = appContext.getSystemService(Context.NSD_SERVICE) as? NsdManager
        if (manager == null) {
            emit(mapOf("type" to "error", "message" to "NSD unavailable"))
            return
        }
        nsdManager = manager
        val listener = object : NsdManager.DiscoveryListener {
            override fun onDiscoveryStarted(serviceType: String) {}
            override fun onStartDiscoveryFailed(serviceType: String, errorCode: Int) {
                running = false
                emit(mapOf("type" to "error", "message" to "discovery failed: $errorCode"))
            }

            override fun onStopDiscoveryFailed(serviceType: String, errorCode: Int) {}

            override fun onDiscoveryStopped(serviceType: String) {
                running = false
            }

            override fun onServiceFound(serviceInfo: NsdServiceInfo) {
                if (!serviceInfo.serviceType.contains("_mako._tcp")) return
                resolve(serviceInfo)
            }

            override fun onServiceLost(serviceInfo: NsdServiceInfo) {
                emit(mapOf("type" to "lost", "service" to serviceInfo.serviceName))
            }
        }
        discoveryListener = listener
        try {
            manager.discoverServices("_mako._tcp", NsdManager.PROTOCOL_DNS_SD, listener)
            running = true
        } catch (e: Exception) {
            discoveryListener = null
            emit(mapOf("type" to "error", "message" to (e.message ?: "discovery failed")))
        }
    }

    private fun resolve(serviceInfo: NsdServiceInfo) {
        if (!resolving.add(serviceInfo.serviceName)) return
        val manager = nsdManager ?: return
        try {
            manager.resolveService(serviceInfo, object : NsdManager.ResolveListener {
                override fun onResolveFailed(info: NsdServiceInfo, errorCode: Int) {
                    resolving.remove(info.serviceName)
                }

                override fun onServiceResolved(info: NsdServiceInfo) {
                    resolving.remove(info.serviceName)
                    val host = info.host?.hostAddress ?: return
                    val attributes = info.attributes
                        ?.mapValues { it.value.toString(Charsets.UTF_8) }
                        ?: emptyMap()
                    val id = attributes["id"] ?: return
                    emit(
                        mapOf(
                            "type" to "found",
                            "service" to info.serviceName,
                            "id" to id,
                            "name" to (attributes["name"] ?: info.serviceName),
                            "host" to host,
                            "port" to info.port,
                            "path" to (attributes["path"] ?: "/api/v1"),
                        )
                    )
                }
            })
        } catch (e: Exception) {
            resolving.remove(serviceInfo.serviceName)
            emit(mapOf("type" to "error", "message" to (e.message ?: "resolve failed")))
        }
    }

    private fun stop() {
        val manager = nsdManager
        val listener = discoveryListener
        if (manager != null && listener != null) {
            try {
                manager.stopServiceDiscovery(listener)
            } catch (_: Exception) {
            }
        }
        nsdManager = null
        discoveryListener = null
        resolving.clear()
        running = false
    }

    private fun emit(event: Map<String, Any?>) {
        mainHandler.post { sink?.success(event) }
    }
}
