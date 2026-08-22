package com.pocketnas.client

import android.content.Intent
import android.os.Bundle
import androidx.appcompat.app.AppCompatActivity
import androidx.fragment.app.Fragment
import com.google.android.material.bottomnavigation.BottomNavigationView
import com.pocketnas.client.ui.files.FilesFragment
import com.pocketnas.client.ui.people.PeopleFragment
import com.pocketnas.client.ui.profile.ProfileFragment
import com.pocketnas.client.ui.server.ServerConnectActivity
import com.pocketnas.client.ui.timeline.TimelineFragment

/**
 * Entry router + 单 Activity 底部 tab 主页（SPEC-M10 §1 / SPEC-M12 §1 / SPEC-M14 §1）：
 * 相册（TimelineFragment）/ 人物（PeopleFragment）/ 文件（FilesFragment）/
 * 我的（ProfileFragment）。未连接服务器时跳转 ServerConnectActivity。
 */
class MainActivity : AppCompatActivity() {

    private var timeline: Fragment? = null
    private var people: Fragment? = null
    private var files: Fragment? = null
    private var profile: Fragment? = null
    private var current: Fragment? = null

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        if (App.of(this).apiClient == null) {
            startActivity(Intent(this, ServerConnectActivity::class.java))
            finish()
            return
        }
        setContentView(R.layout.activity_main)

        val nav: BottomNavigationView = findViewById(R.id.bottom_nav)
        if (savedInstanceState == null) {
            show(tabGallery())
        } else {
            timeline = supportFragmentManager.findFragmentByTag(TAG_TIMELINE)
            people = supportFragmentManager.findFragmentByTag(TAG_PEOPLE)
            files = supportFragmentManager.findFragmentByTag(TAG_FILES)
            profile = supportFragmentManager.findFragmentByTag(TAG_PROFILE)
            current = supportFragmentManager.fragments.firstOrNull { it.isVisible }
        }
        nav.setOnItemSelectedListener { item ->
            when (item.itemId) {
                R.id.tab_gallery -> show(tabGallery())
                R.id.tab_people -> show(tabPeople())
                R.id.tab_files -> show(tabFiles())
                R.id.tab_profile -> show(tabProfile())
            }
            true
        }
    }

    private fun tabGallery(): Fragment = timeline
        ?: TimelineFragment().also { timeline = it }

    private fun tabPeople(): Fragment = people
        ?: PeopleFragment().also { people = it }

    private fun tabFiles(): Fragment = files
        ?: FilesFragment().also { files = it }

    private fun tabProfile(): Fragment = profile
        ?: ProfileFragment().also { profile = it }

    private fun show(target: Fragment) {
        if (current === target) return
        val tx = supportFragmentManager.beginTransaction()
        current?.let { tx.hide(it) }
        if (target.isAdded) {
            tx.show(target)
        } else {
            tx.add(R.id.nav_host, target, tagFor(target))
        }
        tx.commit()
        current = target
    }

    private fun tagFor(f: Fragment): String = when (f) {
        is TimelineFragment -> TAG_TIMELINE
        is PeopleFragment -> TAG_PEOPLE
        is ProfileFragment -> TAG_PROFILE
        else -> TAG_FILES
    }

    companion object {
        private const val TAG_TIMELINE = "tab_timeline"
        private const val TAG_PEOPLE = "tab_people"
        private const val TAG_FILES = "tab_files"
        private const val TAG_PROFILE = "tab_profile"
    }
}
