package com.pocketnas.client.data.discovery

import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Test

class DiscoveryParserTest {

    @Test
    fun `parses valid reply`() {
        val s = Discovery.parseReply("POCKETNAS_HERE|MyNAS|8080|2", "192.168.1.5")
        assertEquals(Discovery.DiscoveredServer("MyNAS", "192.168.1.5", 8080, 2), s)
    }

    @Test
    fun `rejects wrong prefix`() {
        assertNull(Discovery.parseReply("HELLO|x|8080|2", "10.0.0.1"))
    }

    @Test
    fun `rejects malformed fields`() {
        assertNull(Discovery.parseReply("POCKETNAS_HERE|x|notaport|2", "10.0.0.1"))
        assertNull(Discovery.parseReply("POCKETNAS_HERE|x|8080", "10.0.0.1"))
        assertNull(Discovery.parseReply("POCKETNAS_HERE|x|8080|2|extra", "10.0.0.1"))
        assertNull(Discovery.parseReply("POCKETNAS_HERE|x|99999|2", "10.0.0.1"))
        assertNull(Discovery.parseReply("POCKETNAS_HERE|x|0|2", "10.0.0.1"))
    }

    @Test
    fun `trims whitespace and newline`() {
        val s = Discovery.parseReply("POCKETNAS_HERE|n|8080|2\n", "10.0.0.2")
        assertEquals(2, s?.apiLevel)
    }
}
