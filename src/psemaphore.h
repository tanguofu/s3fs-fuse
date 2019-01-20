/*
 * s3fs - FUSE-based file system backed by Amazon S3
 *
 * Copyright(C) 2007 Randy Rizun <rrizun@gmail.com>
 *
 * This program is free software; you can redistribute it and/or
 * modify it under the terms of the GNU General Public License
 * as published by the Free Software Foundation; either version 2
 * of the License, or (at your option) any later version.
 *
 * This program is distributed in the hope that it will be useful,
 * but WITHOUT ANY WARRANTY; without even the implied warranty of
 * MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
 * GNU General Public License for more details.
 *
 * You should have received a copy of the GNU General Public License
 * along with this program; if not, write to the Free Software
 * Foundation, Inc., 51 Franklin Street, Fifth Floor, Boston, MA  02110-1301, USA.
 */

#ifndef S3FS_SEMAPHONE_H_
#define S3FS_SEMAPHONE_H_

// portability wrapper for sem_t since macOS does not implement it

#ifdef __APPLE__

#include <dispatch/dispatch.h>

class Semaphore
{
  public:
    explicit Semaphore(int value) : sem(dispatch_semaphore_create(value)) {}
    ~Semaphore() { dispatch_release(sem); }
    void wait() { dispatch_semaphore_wait(sem, DISPATCH_TIME_FOREVER); }
    void post() { dispatch_semaphore_signal(sem); }
  private:
    dispatch_semaphore_t sem;
};

#else

#include <errno.h>
#include <semaphore.h>

class Semaphore
{
  public:
    explicit Semaphore(int value) { sem_init(&mutex, 0, value); }
    ~Semaphore() { sem_destroy(&mutex); }
    void wait()
    {
      int r;
      do {
        r = sem_wait(&mutex);
      } while (r == -1 && errno == EINTR);
    }
    void post() { sem_post(&mutex); }
  private:
    sem_t mutex;
};

#endif

#endif // S3FS_SEMAPHONE_H_